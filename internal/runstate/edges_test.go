package runstate

import (
	"bytes"
	"errors"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestCatalogFilesystemRejection(t *testing.T) {
	t.Run("invalid root", func(t *testing.T) {
		if err := (Store{Root: "  "}).Ensure(); err == nil { t.Fatal("blank state root accepted") }
		if err := (Store{Root: filepath.Join(t.TempDir(), "wrong-root")}).Ensure(); err == nil {
			t.Fatal("arbitrary state root accepted")
		}
		if err := (Store{Root: filepath.Join(t.TempDir(), "absent", ".distroplane")}).Ensure(); err == nil {
			t.Fatal("nonexistent project accepted")
		}
	})
	t.Run("untrusted directory", func(t *testing.T) {
		for _, part := range []string{"catalog", "plans", "runs"} {
			t.Run(part, func(t *testing.T) {
				store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "release bytes")
				dir := filepath.Join(store.Root, "catalog")
				if part != "catalog" { dir = filepath.Join(dir, part) }
				if runtime.GOOS == "windows" {
					other := t.TempDir()
					if err := os.Remove(dir); err != nil { t.Fatal(err) }
					if err := os.Symlink(other, dir); err != nil { t.Skipf("symlinks unavailable: %v",err) }
				} else if err := os.Chmod(dir, 0777); err != nil { t.Fatal(err) }
				if err := store.Ensure(); err == nil { t.Fatal("unsafe directory accepted") }
			})
		}
	})
	t.Run("existing catalog obstruction", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "release bytes")
		root := filepath.Join(store.Root, "catalog")
		if err := os.RemoveAll(root); err != nil { t.Fatal(err) }
		if err := os.WriteFile(root, []byte("obstructed"), 0600); err != nil { t.Fatal(err) }
		if err := store.Ensure(); err == nil { t.Fatal("file in place of catalog accepted") }
	})
	t.Run("existing missing marker", func(t *testing.T) {
		store, _, _ := fixturePlan(t, filepath.Join(t.TempDir(), "project"), "release bytes")
		marker := filepath.Join(store.Root, ".catalog-required")
		if err := os.Remove(marker); err != nil { t.Fatal(err) }
		if err := store.Ensure(); err != nil { t.Fatalf("safe catalog marker repair failed: %v",err) }
		if fi,err:=os.Stat(marker);err!=nil||!fi.Mode().IsRegular(){t.Fatalf("marker not recreated: %v",err)}
	})
}

func TestCatalogAtomicEntryErrorHandling(t *testing.T) {
	dir := t.TempDir()
	v := entry{SchemaVersion:schemaVersion,PlanID:"plan-one",PlanPath:filepath.Join(dir,"plan.json")}
	if _,err:=install(filepath.Join(dir,"missing"),"new.json",v);err==nil{t.Fatal("missing catalog directory accepted")}
	if _,err:=install(dir,"bad-entry.json",v);err!=nil{t.Fatalf("install: %v",err)}
	damaged:=filepath.Join(dir,"bad-entry.json")
	if err:=os.WriteFile(damaged,[]byte("{bad"),0600);err!=nil{t.Fatal(err)}
	if _,err:=install(dir,"bad-entry.json",v);err==nil{t.Fatal("corrupt preexisting record overwritten")}
	if err:=os.Remove(damaged);err!=nil{t.Fatal(err)}
	if err:=os.Mkdir(damaged,0700);err!=nil{t.Fatal(err)}
	if _,err:=install(dir,"bad-entry.json",v);err==nil{t.Fatal("existing directory replaced with record")}
	if _,err:=readEntry(damaged);err==nil{t.Fatal("directory treated as record")}
	if err:=os.Remove(damaged);err!=nil{t.Fatal(err)}
	if err:=os.Symlink(filepath.Join(dir,"plan.json"),damaged);err==nil{
		if _,err:=readEntry(damaged);err==nil{t.Fatal("symlink treated as record")}
	}
	if _,err:=readEntry(filepath.Join(dir,"never-created"));!errors.Is(err,os.ErrNotExist){t.Fatalf("missing entry: %v",err)}
}
func TestCatalogEntrySchemaAndIdentityValidation(t *testing.T) {
	dir:=t.TempDir()
	paths:=[]string{
		`{"schemaVersion":"1","planId":"","planPath":"/tmp/plan.json"}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"/tmp/plan.json","runId":"run","journalPath":""}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"/tmp/plan.json","runId":"run","journalPath":"relative"}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"/tmp/plan.json","journalPath":"/tmp/journal"}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"relative","runId":"run","journalPath":"/tmp/journal"}`,
		`{"schemaVersion":"1","planId":"plan","planPath":"/tmp/plan.json","runId":"run\ninvalid","journalPath":"/tmp/journal"}`,
	}
	for i,raw:=range paths{
		path:=filepath.Join(dir,"bad-"+string(rune('a'+i))+".json")
		if err:=os.WriteFile(path,[]byte(raw),0600);err!=nil{t.Fatal(err)}
		if _,err:=readEntry(path);err==nil{t.Fatalf("entry %d unexpectedly valid: %s",i,raw)}
	}
}

func TestCatalogPlanEntryAndClaimValidation(t *testing.T) {
	t.Run("missing plan record",func(t *testing.T){
		store,_,id:=fixturePlan(t,filepath.Join(t.TempDir(),"project"),"bytes")
		path:=filepath.Join(store.Root,"catalog","plans",key(string(id)))
		if err:=os.Remove(path);err!=nil{t.Fatal(err)}
		dir,err:=store.directory(false);if err!=nil{t.Fatal(err)}
		if _,err:=store.planEntry(dir,id);err==nil{t.Fatal("missing plan record accepted")}
	})
	t.Run("run fields injected in plan",func(t *testing.T){
		store,_,id:=fixturePlan(t,filepath.Join(t.TempDir(),"project"),"bytes")
		path:=filepath.Join(store.Root,"catalog","plans",key(string(id)))
		v,err:=readEntry(path);if err!=nil{t.Fatal(err)}
		v.RunID="run-injected";v.JournalPath=filepath.Join(store.Root,"journal.bin")
		b,err:=jsonFileForTest(v);if err!=nil{t.Fatal(err)}
		if err:=os.WriteFile(path,b,0600);err!=nil{t.Fatal(err)}
		dir,err:=store.directory(false);if err!=nil{t.Fatal(err)}
		if _,err:=store.planEntry(dir,id);err==nil{t.Fatal("run metadata injected into immutable plan")}
	})
	t.Run("missing claim paths",func(t *testing.T){
		store,path,id:=fixturePlan(t,filepath.Join(t.TempDir(),"project"),"bytes")
		if err:=store.ClaimRun(path,"","run-valid");err==nil{t.Fatal("missing journal path accepted")}
		if err:=store.ClaimRun("","/tmp/unused","run-valid");err==nil{t.Fatal("missing plan path accepted")}
		dir,err:=store.directory(false);if err!=nil{t.Fatal(err)}
		if e,exists,err:=store.claimEntry(dir,id);err!=nil||exists||e.RunID!=""{t.Fatalf("unclaimed run %+v %t %v",e,exists,err)}
	})
	t.Run("tampered registered plan",func(t *testing.T){
		store,path,id:=fixturePlan(t,filepath.Join(t.TempDir(),"project"),"bytes")
		b,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
		if err:=os.WriteFile(path,append(bytes.Clone(b),byte(' ')),0600);err!=nil{t.Fatal(err)}
		if _,err:=store.RegisterPlan(path);err==nil{t.Fatal("mutated plan registered")}
		_ = id
	})
}
func jsonFileForTest(v entry)([]byte,error){
	b,err:=json.Marshal(v)
	if err!=nil{return nil,err}
	return b,nil
}
func TestCanonicalFileParentObstructions(t *testing.T) {
	root:=t.TempDir()
	regular:=filepath.Join(root,"blocked")
	if err:=os.WriteFile(regular,[]byte("a"),0600);err!=nil{t.Fatal(err)}
	if _,err:=canonicalFile(filepath.Join(regular,"journal"),false);err==nil{t.Fatal("file used as parent directory")}
	missing:=filepath.Join(root,"not-created","nested","journal")
	got,err:=canonicalFile(missing,false)
	if err!=nil||got==""{t.Fatalf("uncreated journal parent should be reservable: %q %v",got,err)}
	if _,err:=canonicalFile(missing,true);err==nil{t.Fatal("missing required journal permitted")}
}
func TestCatalogMultipleSelectionsAreStable(t *testing.T) {
	root:=filepath.Join(t.TempDir(),"project")
	store,_,first:=fixturePlan(t,root,"first content")
	_,_,second:=fixturePlan(t,root,"second content")
	if first==second{t.Fatal("fixtures did not generate distinct plan IDs")}
	records,err:=store.Inventory()
	if err!=nil||len(records)!=2{t.Fatalf("two plans missing: %+v, %v",records,err)}
	if records[0].PlanID>records[1].PlanID{t.Fatal("inventory not sorted deterministically")}
	if _,err:=store.Select("release-1","");!errors.Is(err,ErrAmbiguous){t.Fatalf("nonunique release selected: %v",err)}
}

func TestCatalogJournalMismatchFailsClosed(t *testing.T) {
	store,path,id:=fixturePlan(t,filepath.Join(t.TempDir(),"project"),"bytes")
	j:=filepath.Join(store.Root,"old.journal")
	startJournal(t,j,domain.RunID("run-one"),id)
	incorrect := entry{SchemaVersion:schemaVersion,PlanID:string(id),PlanPath:path,RunID:"another-run",JournalPath:j}
	if _,err:=readJournal(incorrect);!errors.Is(err,ErrConflict){t.Fatalf("mismatched run permitted: %v",err)}
	if err:=store.ClaimRun(path,j,domain.RunID("another-run"));!errors.Is(err,ErrConflict){t.Fatalf("conflicting initial journal permitted: %v",err)}
}
