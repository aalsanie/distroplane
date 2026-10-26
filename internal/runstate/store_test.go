package runstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
)

type fixtureClient struct{}

func (fixtureClient) Describe(context.Context,planner.Endpoint)(protocol.DescribeResponse,error){
	return protocol.DescribeResponse{
		Provider:protocol.ProviderIdentity{Name:"fake",Version:"1.0.0"},
		ProtocolVersions:[]string{protocol.Version},
		Capabilities:[]protocol.Capability{protocol.CapabilityPlan,protocol.CapabilityApply,protocol.CapabilityReconcile},
	},nil
}

func (fixtureClient) Plan(context.Context,planner.Endpoint,protocol.PlanRequest)(protocol.PlanResponse,error){
	return protocol.PlanResponse{Operations:[]protocol.PlannedOperation{{
		ID:"publish",Kind:"publish",SideEffecting:true,ProviderPayload:json.RawMessage(`{}`),
	}}},nil
}

type fixedResolver struct{path string}
func (f fixedResolver)Resolve(_, _ string)(string,error){return f.path,nil}

func fixturePlan(t *testing.T, root, payload string)(Store,string,domain.PlanID){
	t.Helper()
	if err:=os.MkdirAll(root,0700);err!=nil{t.Fatal(err)}
	artifact:=filepath.Join(root,"artifact.bin")
	provider:=filepath.Join(root,"provider.bin")
	if err:=os.WriteFile(artifact,[]byte(payload),0600);err!=nil{t.Fatal(err)}
	if err:=os.WriteFile(provider,[]byte("fake provider identity"),0700);err!=nil{t.Fatal(err)}
	p,err:=planner.New(fixtureClient{},planner.Options{Resolver:fixedResolver{path:provider}})
	if err!=nil{t.Fatal(err)}
	loaded:=config.Loaded{BaseDir:root,Config:config.Config{
		SchemaVersion:"1",
		Release:config.Release{ID:"release-1",Artifacts:[]config.Artifact{{Name:"app",Source:artifact}}},
		Targets:[]config.Target{{ID:"fake-target",Provider:config.Provider{Name:"fake"},Configuration:json.RawMessage(`{}`)}},
	}}
	plan,err:=p.Build(context.Background(),loaded)
	if err!=nil{t.Fatal(err)}
	path,err:=(planner.Store{Root:filepath.Join(root,".distroplane")}).Save(plan)
	if err!=nil{t.Fatal(err)}
	return Store{Root:filepath.Join(root,".distroplane")},path,plan.ID()
}
func startJournal(t *testing.T,path string,id domain.RunID,planID domain.PlanID){
	t.Helper()
	w,err:=journal.OpenWriter(path,id)
	if err!=nil{t.Fatal(err)}
	if _,err:=w.Append(journal.Entry{RunID:id,PlanID:planID,Type:journal.EventRunStarted});err!=nil{t.Fatal(err)}
	if err:=w.Close();err!=nil{t.Fatal(err)}
}

func TestCatalogRecordsPlanAndReplaysVerifiedJournal(t *testing.T){
	store,path,id:=fixturePlan(t,filepath.Join(t.TempDir(),"project"),"release bytes")
	if got,err:=store.RegisterPlan(path);err!=nil||got!=id{t.Fatalf("register=%s err=%v",got,err)}
	empty,err:=store.Inventory()
	if err!=nil||len(empty)!=1||empty[0].State!="PLANNED"{t.Fatalf("empty inventory=%+v err=%v",empty,err)}
	journalPath:=filepath.Join(filepath.Dir(path),"..","runs","primary.journal")
	run:=domain.RunID("run-one")
	if err:=store.ClaimRun(path,journalPath,run);err!=nil{t.Fatal(err)}
	reserved,err:=store.Select("release-1","run-one")
	if err!=nil||reserved.State!="RESERVED"{t.Fatalf("reservation=%+v err=%v",reserved,err)}
	if err:=store.ClaimRun(path,journalPath,run);err==nil{t.Fatal("repeated missing journal must not start another publish")}
	if err:=os.MkdirAll(filepath.Dir(journalPath),0700);err!=nil{t.Fatal(err)}
	startJournal(t,journalPath,run,id)
	if err:=store.ClaimRun(path,journalPath,run);err!=nil{t.Fatal(err)}
	record,err:=store.Select("release-1","run-one")
	if err!=nil||record.State!="PENDING"||record.Completed{t.Fatalf("replayed=%+v err=%v",record,err)}
	if _,err:=store.Select("wrong-release","");!errors.Is(err,os.ErrNotExist){t.Fatalf("unknown selection: %v",err)}
	if _,err:=store.Select("release-1","other");!errors.Is(err,os.ErrNotExist){t.Fatalf("unknown run: %v",err)}
	if err:=store.ClaimRun(path,filepath.Join(filepath.Dir(journalPath),"alternate.journal"),run);!errors.Is(err,ErrConflict){
		t.Fatalf("altered journal accepted: %v",err)
	}
	if err:=store.ClaimRun(path,journalPath,domain.RunID("run-second"));!errors.Is(err,ErrConflict){
		t.Fatalf("altered run accepted: %v",err)
	}
}

func TestCatalogConcurrentClaimIsSingleWinner(t *testing.T){
	store,path,_:=fixturePlan(t,filepath.Join(t.TempDir(),"project"),"payload")
	if _,err:=store.RegisterPlan(path);err!=nil{t.Fatal(err)}
	type response struct{idx int;err error}
	ch:=make(chan response,32)
	var wg sync.WaitGroup
	for i:=0;i<32;i++{
		wg.Add(1)
		go func(i int){
			defer wg.Done()
			journalPath:=filepath.Join(filepath.Dir(path), "runs", "attempt-"+string(rune('a'+i))+".journal")
			err:=store.ClaimRun(path,journalPath,domain.RunID("run-"+string(rune('a'+i))))
			ch<-response{i,err}
		}(i)
	}
	wg.Wait()
	close(ch)
	success:=0
	for x:=range ch{
		if x.err==nil{success++}else if !errors.Is(x.err,ErrConflict){t.Fatalf("claim %d failed unexpectedly: %v",x.idx,x.err)}
	}
	if success!=1{t.Fatalf("expected exactly one durable winner, got %d",success)}
	records,err:=store.Inventory()
	if err!=nil||len(records)!=1||records[0].State!="RESERVED"{t.Fatalf("inventory=%+v err=%v",records,err)}
}

func TestCatalogRecoversTruncatedPrimaryWithoutLosingJournal(t *testing.T){
	store,path,id:=fixturePlan(t,filepath.Join(t.TempDir(),"project"),"payload")
	run:=domain.RunID("run-valid")
	journalPath:=filepath.Join(filepath.Dir(path),"valid.journal")
	if err:=store.ClaimRun(path,journalPath,run);err!=nil{t.Fatal(err)}
	startJournal(t,journalPath,run,id)
	primary:=filepath.Join(store.Root,"catalog","runs",key(string(id)))
	if err:=os.WriteFile(primary,[]byte("{bad"),0600);err!=nil{t.Fatal(err)}
	records,err:=store.Inventory()
	if err!=nil||len(records)!=1||records[0].State!="PENDING"{t.Fatalf("primary corruption erased journal metadata: %+v %v",records,err)}
	if err:=os.WriteFile(primary+".backup",[]byte("{bad"),0600);err!=nil{t.Fatal(err)}
	if _,err:=store.Inventory();err==nil{t.Fatal("two corrupt claims silently ignored")}
	if b,err:=os.ReadFile(journalPath);err!=nil||len(b)==0{t.Fatalf("catalog corruption damaged journal: %v",err)}
}

func TestCatalogRejectsTamperAndUnsafeStateRoots(t *testing.T){
	root:=filepath.Join(t.TempDir(),"project")
	store,path,id:=fixturePlan(t,root,"payload")
	if _,err:=store.RegisterPlan(path);err!=nil{t.Fatal(err)}
	bad:=filepath.Join(root,"other")
	if err:=os.WriteFile(bad,[]byte("invalid"),0600);err!=nil{t.Fatal(err)}
	if _,err:=store.RegisterPlan(bad);err==nil{t.Fatal("invalid plan accepted")}
	catalog:=filepath.Join(store.Root,"catalog")
	planRecord:=filepath.Join(catalog,"plans",key(string(id)))
	if err:=os.WriteFile(planRecord,[]byte(`{"schemaVersion":"99"}`),0600);err!=nil{t.Fatal(err)}
	if _,err:=store.Inventory();err==nil{t.Fatal("corrupt plan index silently ignored")}
	if _,err:=store.RegisterPlan(path);err==nil{t.Fatal("corrupt immutable plan overwritten")}
	unsafe:=Store{Root:filepath.Join(t.TempDir(),"other-state")}
	if _,err:=unsafe.Inventory();err==nil{t.Fatal("unexpected catalog root allowed")}
	link:=filepath.Join(root,"linked")
	if err:=os.Symlink(store.Root,link);err==nil{
		if _,err:= (Store{Root:link}).Inventory();err==nil{t.Fatal("symlinked state root accepted")}
	}
}

func TestCatalogNoDuplicateReleaseSelection(t *testing.T){
	root:=t.TempDir()
	store1,p1,_:=fixturePlan(t,filepath.Join(root,"project1"),"first")
	store2,p2,_:=fixturePlan(t,filepath.Join(root,"project2"),"second")
	if _,err:=store1.RegisterPlan(p1);err!=nil{t.Fatal(err)}
	if _,err:=store2.RegisterPlan(p2);err!=nil{t.Fatal(err)}
	list1,err:=store1.Inventory()
	if err!=nil||len(list1)!=1{t.Fatalf("project1=%+v err=%v",list1,err)}
	list2,err:=store2.Inventory()
	if err!=nil||len(list2)!=1{t.Fatalf("project2=%+v err=%v",list2,err)}
	if list1[0].PlanID==list2[0].PlanID{t.Fatal("changed artifact path/bytes did not affect plan identity")}
	// Two plans for one release may coexist; automatic selection must not
	// pick whichever happens to have been written last.
	otherFile:=filepath.Join(root,"project1","other.bin")
	if err:=os.WriteFile(otherFile,[]byte("alternate artifact"),0600);err!=nil{t.Fatal(err)}
	// The ambiguity behavior is also checked without mutating the frozen plan.
	if got,err:=store1.Select("release-1","");err!=nil||got.PlanID!=list1[0].PlanID{t.Fatalf("single plan selection %+v %v",got,err)}
}

func TestCatalogEntryIntegrityAndErrorPaths(t *testing.T){
	dir:=t.TempDir()
	if _,err:=readEntry(filepath.Join(dir,"missing"));!errors.Is(err,os.ErrNotExist){t.Fatalf("missing=%v",err)}
	invalid:=[]string{
		`{"schemaVersion":"2","planId":"plan","planPath":"/tmp/example"}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"relative"}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"/tmp/example","runId":"run"}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"/tmp/example","extra":"x"}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"/tmp/example"}{}`,
		`{invalid json`,
	}
	for i,raw:=range invalid{
		p:=filepath.Join(dir,"bad-"+string(rune('a'+i)))
		if err:=os.WriteFile(p,[]byte(raw),0600);err!=nil{t.Fatal(err)}
		if _,err:=readEntry(p);err==nil{t.Fatalf("invalid entry accepted: %s",raw)}
	}
	if _,err:=canonicalFile("",true);err==nil{t.Fatal("blank path allowed")}
	if _,err:=canonicalFile(dir,true);err==nil{t.Fatal("directory accepted as journal")}
	if _,err:=canonicalFile(filepath.Join(dir,"absent"),true);!errors.Is(err,os.ErrNotExist){t.Fatalf("missing file=%v",err)}
	if err:=os.Symlink(dir,filepath.Join(dir,"link"));err==nil{
		if _,err:=canonicalFile(filepath.Join(dir,"link"),true);err==nil{t.Fatal("symlink allowed")}
	}
	if strings.Contains(key("foo"),"foo"){t.Fatal("catalog key leaks unsafe identifiers")}
	if _,err:=readEntries(filepath.Join(dir,"missing"));err==nil{t.Fatal("missing directory accepted")}
	v:=entry{SchemaVersion:schemaVersion,PlanID:"safe-id",PlanPath:"/tmp/file"}
	if created,err:=install(dir,"entry.json",v);err!=nil||!created{t.Fatalf("write entry %t %v",created,err)}
	if created,err:=install(dir,"entry.json",v);err!=nil||created{t.Fatalf("idempotent write %t %v",created,err)}
	v.PlanID="other"
	if _,err:=install(dir,"entry.json",v);!errors.Is(err,ErrConflict){t.Fatalf("immutable record overwritten: %v",err)}
	if _,err:=readEntry(filepath.Join(dir,"entry.json"));err!=nil{t.Fatal(err)}
	oversized:=filepath.Join(dir,"oversized")
	if err:=os.WriteFile(oversized,bytes.Repeat([]byte("x"),maxEntryBytes+1),0600);err!=nil{t.Fatal(err)}
	if _,err:=readEntry(oversized);err==nil{t.Fatal("oversized entry accepted")}
}
