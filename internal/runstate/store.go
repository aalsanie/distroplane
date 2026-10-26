package runstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/planner"
)

const schemaVersion = "1"
const maxEntryBytes = 16 << 10

var ErrAmbiguous = errors.New("ambiguous release selection")
var ErrConflict = errors.New("run already bound to a different journal or identity")

// Store uses immutable, atomic directory entries rather than an authoritative
// mutable index. The duplicate run claim is written first; it prevents a second
// dispatch if the process stops between catalog writes and journal creation.
type Store struct { Root string }

type entry struct {
	SchemaVersion string `json:"schemaVersion"`
	PlanID string `json:"planId"`
	PlanPath string `json:"planPath"`
	RunID string `json:"runId,omitempty"`
	JournalPath string `json:"journalPath,omitempty"`
}

type Record struct {
	ReleaseID string `json:"releaseId"`
	PlanID string `json:"planId"`
	PlanPath string `json:"planPath"`
	RunID string `json:"runId,omitempty"`
	JournalPath string `json:"journalPath,omitempty"`
	State string `json:"state"`
	Completed bool `json:"completed"`
	ReconcileRequired bool `json:"reconcileRequired"`
}

func key(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:]) + ".json"
}

func (s Store) directory(create bool) (string, error) {
	if strings.TrimSpace(s.Root) == "" {
		return "", fmt.Errorf("catalog root is required")
	}
	root, err := filepath.Abs(s.Root)
	if err != nil { return "", err }
	project, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil { return "", fmt.Errorf("resolve catalog project: %w", err) }
	root = filepath.Join(project, filepath.Base(root))
	if filepath.Base(root) != ".distroplane" {
		return "", fmt.Errorf("catalog must be inside the project .distroplane directory")
	}
	catalog := filepath.Join(root, "catalog")
	dirs := []string{root, catalog, filepath.Join(catalog, "plans"), filepath.Join(catalog, "runs")}
	for i, dir := range dirs {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) && create {
			if err = os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) { return "", err }
			info, err = os.Lstat(dir)
		}
		if errors.Is(err, os.ErrNotExist) && !create {
			return "", os.ErrNotExist
		}
		if err != nil { return "", err }
		if !info.IsDir() { return "", fmt.Errorf("unsafe catalog directory %q", dir) }
		// An existing .distroplane directory is historically 0755. All new
		// catalog directories must be private to the current OS account.
		if i > 0 && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return "", fmt.Errorf("catalog directory %q must be private", dir)
		}
	}
	return catalog, nil
}

func canonicalFile(path string, mustExist bool) (string, error) {
	if strings.TrimSpace(path) == "" { return "", fmt.Errorf("file path is required") }
	absolute, err := filepath.Abs(path)
	if err != nil { return "", err }
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil { return "", err }
	absolute = filepath.Join(parent, filepath.Base(absolute))
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) && !mustExist { return absolute, nil }
	if err != nil { return "", err }
	if !info.Mode().IsRegular() { return "", fmt.Errorf("%q must be a regular file, not a symlink or special file", absolute) }
	return absolute, nil
}

func directorySync(dir string) error {
	if runtime.GOOS == "windows" { return nil }
	f, err := os.Open(dir)
	if err != nil { return err }
	defer f.Close()
	return f.Sync()
}

func install(dir, name string, value entry) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil { return false, err }
	raw = append(raw, '\n')
	target := filepath.Join(dir, name)
	temp, err := os.CreateTemp(dir, ".entry-*")
	if err != nil { return false, err }
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0600); err != nil { temp.Close(); return false, err }
	if _, err := temp.Write(raw); err != nil { temp.Close(); return false, err }
	if err := temp.Sync(); err != nil { temp.Close(); return false, err }
	if err := temp.Close(); err != nil { return false, err }
	if err := os.Link(temp.Name(), target); err != nil {
		previous, readErr := readEntry(target)
		if readErr != nil { return false, fmt.Errorf("catalog entry %q cannot be read: %w", target, readErr) }
		if previous != value { return false, fmt.Errorf("%w: %s", ErrConflict, target) }
		return false, nil
	}
	if err := directorySync(dir); err != nil { return true, err }
	return true, nil
}

func readEntry(path string) (entry, error) {
	info, err := os.Lstat(path)
	if err != nil { return entry{}, err }
	if !info.Mode().IsRegular() || info.Size() > maxEntryBytes {
		return entry{}, fmt.Errorf("unsafe catalog entry %q", path)
	}
	b, err := os.ReadFile(path)
	if err != nil { return entry{}, err }
	if len(b) > maxEntryBytes { return entry{}, fmt.Errorf("oversized catalog entry %q", path) }
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var value entry
	if err := dec.Decode(&value); err != nil { return entry{}, fmt.Errorf("corrupt catalog entry %q: %w", path, err) }
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) { return entry{}, fmt.Errorf("trailing catalog entry data in %q", path) }
	if value.SchemaVersion != schemaVersion || !domain.PlanID(value.PlanID).Valid() {
		return entry{}, fmt.Errorf("unsupported catalog identity in %q", path)
	}
	if value.RunID == "" && value.JournalPath != "" || value.RunID != "" && (value.JournalPath == "" || !domain.RunID(value.RunID).Valid()) {
		return entry{}, fmt.Errorf("invalid run identity in %q", path)
	}
	if !filepath.IsAbs(value.PlanPath) || (value.JournalPath != "" && !filepath.IsAbs(value.JournalPath)) {
		return entry{}, fmt.Errorf("relative catalog paths in %q", path)
	}
	return value, nil
}

func (s Store) RegisterPlan(path string) (domain.PlanID, error) {
	path, err := canonicalFile(path, true)
	if err != nil { return "", err }
	plan, err := planner.Load(path)
	if err != nil { return "", err }
	if err := plan.VerifyProviderDigests(); err != nil { return "", err }
	dir, err := s.directory(true)
	if err != nil { return "", err }
	id := plan.ID()
	e := entry{SchemaVersion:schemaVersion, PlanID:string(id), PlanPath:path}
	if _, err := install(filepath.Join(dir,"plans"),key(string(id)),e); err != nil { return "", err }
	return id, nil
}

func (s Store) planEntry(dir string, id domain.PlanID) (entry, error) {
	e, err := readEntry(filepath.Join(dir, "plans", key(string(id))))
	if err != nil { return entry{}, err }
	if e.PlanID != string(id) || e.RunID != "" || e.JournalPath != "" { return entry{}, fmt.Errorf("catalog plan record mismatch for %q", id) }
	return e, nil
}

func (s Store) claimEntry(dir string, id domain.PlanID) (entry, bool, error) {
	base := filepath.Join(dir, "runs", key(string(id)))
	primary, err1 := readEntry(base)
	backup, err2 := readEntry(base+".backup")
	if errors.Is(err1, os.ErrNotExist) && errors.Is(err2, os.ErrNotExist) { return entry{}, false, nil }
	if err1 == nil && err2 == nil {
		if primary != backup { return entry{}, true, fmt.Errorf("%w: conflicting run records for %q", ErrConflict, id) }
	} else if err1 == nil {
		backup = primary
	} else if err2 == nil {
		primary = backup
	} else { return entry{}, true, fmt.Errorf("corrupt run catalog for %q: primary=%v backup=%v", id, err1, err2) }
	if primary.PlanID != string(id) || !domain.RunID(primary.RunID).Valid() || primary.JournalPath == "" {
		return entry{}, true, fmt.Errorf("invalid run record for %q", id)
	}
	return primary, true, nil
}

// ClaimRun performs the durable claim before any irreversible provider call.
// Exactly one run may be claimed for each immutable plan; a different journal
// or run ID is never selected automatically, even after completion.
func (s Store) ClaimRun(planPath, journalPath string, runID domain.RunID) error {
	if !runID.Valid() { return fmt.Errorf("run ID is invalid") }
	planPath, err := canonicalFile(planPath, true)
	if err != nil { return err }
	journalPath, err = canonicalFile(journalPath, false)
	if err != nil { return err }
	id, err := s.RegisterPlan(planPath)
	if err != nil { return err }
	dir, err := s.directory(true)
	if err != nil { return err }
	p, err := s.planEntry(dir,id)
	if err != nil { return err }
	if p.PlanPath != planPath { return fmt.Errorf("%w: plan changed location",ErrConflict) }
	claim := entry{SchemaVersion:schemaVersion, PlanID:string(id), PlanPath:planPath, RunID:string(runID), JournalPath:journalPath}
	// Never create an alternate publication run if this path contains an
	// unreadable/invalid historical journal.
	existing, err := readJournal(claim)
	if err != nil && !errors.Is(err,os.ErrNotExist) { return err }
	base := filepath.Join(dir,"runs")
	backupName := key(string(id))+".backup"
	created, err := install(base,backupName,claim)
	if err != nil { return err }
	_, err = install(base,key(string(id)),claim)
	if err != nil { return err }
	if !created && errors.Is(err,os.ErrNotExist) { return fmt.Errorf("reserved run lacks journal; manual recovery required") }
	return nil
}

func readJournal(e entry) (journal.ReadResult,error) {
	path,err := canonicalFile(e.JournalPath,true)
	if err != nil { return journal.ReadResult{},err }
	if path != e.JournalPath { return journal.ReadResult{}, fmt.Errorf("journal path changed") }
	f,err := os.Open(path)
	if err != nil { return journal.ReadResult{},err }
	defer f.Close()
	result,err := journal.Read(f)
	if err != nil { return journal.ReadResult{},err }
	if len(result.Events)==0 { return journal.ReadResult{},fmt.Errorf("journal contains no durable run identity") }
	if string(result.Events[0].RunID)!=e.RunID || string(result.Events[0].PlanID)!=e.PlanID {
		return journal.ReadResult{},fmt.Errorf("%w: journal plan/run identity mismatch",ErrConflict)
	}
	return result,nil
}

func readEntries(dir string) ([]entry,error) {
	files,err:=os.ReadDir(dir)
	if err != nil { return nil,err }
	entries:=make([]entry,0,len(files))
	for _,f:=range files {
		name:=f.Name()
		if strings.HasPrefix(name,".entry-") || strings.HasSuffix(name,".backup") { continue }
		if !strings.HasSuffix(name,".json") { return nil,fmt.Errorf("unexpected catalog file %q",name) }
		e,err:=readEntry(filepath.Join(dir,name))
		if err!=nil { return nil,err }
		if key(e.PlanID)!=name { return nil,fmt.Errorf("catalog key mismatch for %q",name) }
		entries=append(entries,e)
	}
	return entries,nil
}

// Inventory never trusts completion flags in the catalog: it replays the
// verified plan and its journal, treating missing journals as reserved state.
func (s Store) Inventory() ([]Record,error) {
	dir,err:=s.directory(false)
	if errors.Is(err,os.ErrNotExist) { return []Record{},nil }
	if err!=nil{return nil,err}
	plans,err:=readEntries(filepath.Join(dir,"plans"))
	if err!=nil{return nil,err}
	runs,err:=readEntries(filepath.Join(dir,"runs"))
	if err!=nil{return nil,err}
	byPlan:=make(map[string]entry,len(runs))
	for _,e:=range runs {
		confirmed,exists,err:=s.claimEntry(dir,domain.PlanID(e.PlanID))
		if err!=nil{return nil,err}
		if !exists || confirmed!=e {return nil,fmt.Errorf("unverified run catalog entry for %q",e.PlanID)}
		byPlan[e.PlanID]=e
	}
	// A crash immediately after the backup claim must still be discoverable.
	backups,err:=os.ReadDir(filepath.Join(dir,"runs"))
	if err!=nil{return nil,err}
	for _,file:=range backups {
		if !strings.HasSuffix(file.Name(),".json.backup"){continue}
		e,err:=readEntry(filepath.Join(dir,"runs",file.Name()))
		if err!=nil{return nil,err}
		if key(e.PlanID)+".backup"!=file.Name(){return nil,fmt.Errorf("invalid run claim filename")}
		confirmed,exists,err:=s.claimEntry(dir,domain.PlanID(e.PlanID))
		if err!=nil{return nil,err}
		if !exists{return nil,fmt.Errorf("lost run claim")}
		byPlan[e.PlanID]=confirmed
	}
	list:=make([]Record,0,len(plans))
	for _,e:=range plans {
		planPath,err:=canonicalFile(e.PlanPath,true)
		if err!=nil{return nil,err}
		if planPath!=e.PlanPath{return nil,fmt.Errorf("plan path moved for %q",e.PlanID)}
		p,err:=planner.Load(planPath)
		if err!=nil{return nil,err}
		if string(p.ID())!=e.PlanID{return nil,fmt.Errorf("saved plan identity mismatch")}
		if err:=p.VerifyProviderDigests();err!=nil{return nil,err}
		rec:=Record{ReleaseID:string(p.Plan().Release().ID()),PlanID:e.PlanID,PlanPath:e.PlanPath,State:"PLANNED"}
		if c,ok:=byPlan[e.PlanID];ok {
			if c.PlanPath!=e.PlanPath{return nil,fmt.Errorf("run references another plan path")}
			rec.RunID=c.RunID;rec.JournalPath=c.JournalPath
			result,err:=readJournal(c)
			if errors.Is(err,os.ErrNotExist){ rec.State="RESERVED" } else if err!=nil {return nil,err} else {
				state,err:=journal.Reduce(p.Plan(),result.Events)
				if err!=nil{return nil,err}
				rec.Completed=state.Completed
				if state.Completed {rec.State="COMPLETED"} else {rec.State="PENDING"}
				for _,op:=range state.Operations(){if op.ReconcileRequired||op.Ambiguous {rec.ReconcileRequired=true}}
			}
		}
		list=append(list,rec)
		delete(byPlan,e.PlanID)
	}
	if len(byPlan)!=0{return nil,fmt.Errorf("orphan run catalog entries")}
	sort.Slice(list,func(i,j int)bool {if list[i].ReleaseID!=list[j].ReleaseID{return list[i].ReleaseID<list[j].ReleaseID};return list[i].PlanID<list[j].PlanID})
	return list,nil
}

// Select fails on any ambiguity; neither timestamps nor directory traversal
// order are acceptable automatic release-selection rules.
func (s Store) Select(releaseID,runID string) (Record,error) {
	records,err:=s.Inventory()
	if err!=nil{return Record{},err}
	var candidates []Record
	for _,r:=range records {
		if releaseID!=""&&r.ReleaseID!=releaseID{continue}
		if runID!=""&&r.RunID!=runID{continue}
		candidates=append(candidates,r)
	}
	if len(candidates)==0{return Record{},os.ErrNotExist}
	if len(candidates)!=1 {
		ids:=make([]string,0,len(candidates))
		for _,r:=range candidates {ids=append(ids,r.PlanID+"/"+r.RunID)}
		return Record{},fmt.Errorf("%w: candidates %s; specify a release and run",ErrAmbiguous,strings.Join(ids,", "))
	}
	return candidates[0],nil
}
