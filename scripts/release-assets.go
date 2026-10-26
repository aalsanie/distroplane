//go:build !ignore

// Release asset assembly and verification. This tool is intentionally stdlib-only.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxExecutableSize = 128 << 20

var platforms = []struct{ os, arch string }{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}
var executables = []string{
	"distroplane", "distroplane-provider-homebrew", "distroplane-provider-npm",
	"distroplane-provider-sdkman", "distroplane-provider-winget",
}

type fileRecord struct {
	Path string `json:"path"`
	SHA256 string `json:"sha256"`
	Size int64 `json:"size"`
	Mode string `json:"mode"`
}
type packageRecord struct {
	SchemaVersion string `json:"schemaVersion"`
	Version string `json:"version"`
	Commit string `json:"commit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	CGOEnabled bool `json:"cgoEnabled"`
	OS string `json:"os"`
	Arch string `json:"arch"`
	Files []fileRecord `json:"files"`
}
type assetRecord struct {
	Name string `json:"name"`
	SHA256 string `json:"sha256"`
	Size int64 `json:"size"`
}
type releaseRecord struct {
	SchemaVersion string `json:"schemaVersion"`
	Version string `json:"version"`
	Commit string `json:"commit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	CGOEnabled bool `json:"cgoEnabled"`
	Assets []assetRecord `json:"assets"`
}
type packedFile struct {
	name string
	data []byte
	mode int64
}
func main() {
	if len(os.Args) != 2 || (os.Args[1] != "build" && os.Args[1] != "verify") {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/release-assets.go build|verify")
		os.Exit(2)
	}
	out := os.Getenv("OUT_DIR")
	if out == "" { out = "dist" }
	var err error
	if os.Args[1] == "build" {
		err = build(out, os.Getenv("VERSION"), os.Getenv("COMMIT"), os.Getenv("BUILD_DATE"), os.Getenv("GO_VERSION"))
	} else {
		err = verify(out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release assets:", err)
		os.Exit(1)
	}
}
func validIdentity(version, commit, date, goVersion string) error {
	if !regexp.MustCompile(`^(0\.0\.0-dev|[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?)$`).MatchString(version) {
		return fmt.Errorf("invalid release version %q", version)
	}
	if !regexp.MustCompile(`^([0-9a-f]{12}|unknown)$`).MatchString(commit) {
		return fmt.Errorf("invalid source commit %q", commit)
	}
	if _, err := time.Parse(time.RFC3339, date); err != nil {
		return fmt.Errorf("invalid build date: %w", err)
	}
	if !regexp.MustCompile(`^go[0-9]+\.[0-9]+(\.[0-9]+)?([a-z0-9.]*)?$`).MatchString(goVersion) {
		return fmt.Errorf("invalid Go version %q", goVersion)
	}
	return nil
}
func assetName(name, version, osName, arch string) string {
	extension := ""
	if osName == "windows" { extension = ".exe" }
	return fmt.Sprintf("%s_%s_%s_%s%s", name, version, osName, arch, extension)
}
func archiveName(version, osName, arch string) string {
	ext := ".tar.gz"
	if osName == "windows" { ext = ".zip" }
	return fmt.Sprintf("distroplane_%s_%s_%s%s", version, osName, arch, ext)
}
func innerName(name, osName string) string {
	if osName == "windows" { name += ".exe" }
	if name == "distroplane" || name == "distroplane.exe" { return "bin/" + name }
	return "libexec/" + name
}
func jsonFile(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil { return nil, err }
	return append(b, '
'), nil
}
func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func readRegular(path string, limit int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil { return nil, err }
	if !fi.Mode().IsRegular() || fi.Size() > limit { return nil, fmt.Errorf("not a bounded regular file: %s", path) }
	b, err := os.ReadFile(path)
	if err != nil { return nil, err }
	if int64(len(b)) != fi.Size() { return nil, fmt.Errorf("file changed while reading: %s", path) }
	return b, nil
}
func writeAtomic(path string, b []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil { return err }
	defer os.Remove(file.Name())
	if _, err = file.Write(b); err != nil { file.Close(); return err }
	if err = file.Sync(); err != nil { file.Close(); return err }
	if err = file.Close(); err != nil { return err }
	return os.Rename(file.Name(), path)
}
func archiveContents(root, version, commit, date, goVersion, osName, arch string) ([]packedFile, error) {
	m := packageRecord{"1",version,commit,date,goVersion,false,osName,arch,nil}
	files := make([]packedFile,0,6)
	for _, executable := range executables {
		name := assetName(executable,version,osName,arch)
		data,err := readRegular(filepath.Join(root,name),maxExecutableSize)
		if err != nil { return nil, fmt.Errorf("read %s: %w",name,err) }
		inner:=innerName(executable,osName)
		m.Files=append(m.Files,fileRecord{inner,hash(data),int64(len(data)),"0755"})
		files=append(files,packedFile{inner,data,0755})
	}
	meta,err:=jsonFile(m)
	if err != nil { return nil,err }
	files=append(files,packedFile{"PACKAGE-METADATA.json",meta,0644})
	sort.Slice(files,func(i,j int)bool{return files[i].name<files[j].name})
	return files,nil
}
func makeArchive(files []packedFile, osName string) ([]byte,error) {
	var out strings.Builder
	// ZIP and tar.gz are binary-safe because strings.Builder preserves raw bytes.
	if osName=="windows" {
		z:=zip.NewWriter(&out)
		for _,f:=range files {
			h:=&zip.FileHeader{Name:f.name,Method:zip.Deflate}
			h.SetMode(os.FileMode(f.mode))
			h.SetModTime(time.Date(1980,1,1,0,0,0,0,time.UTC))
			w,err:=z.CreateHeader(h)
			if err!=nil { return nil,err }
			if _,err=w.Write(f.data);err!=nil { return nil,err }
		}
		if err:=z.Close();err!=nil{return nil,err}
	} else {
		g:=gzip.NewWriter(&out)
		g.Header.ModTime=time.Unix(0,0)
		g.Header.OS=255
		t:=tar.NewWriter(g)
		for _,f:=range files {
			h:=&tar.Header{Name:f.name,Typeflag:tar.TypeReg,Mode:f.mode,Size:int64(len(f.data)),ModTime:time.Unix(0,0),Uid:0,Gid:0,Format:tar.FormatUSTAR}
			if err:=t.WriteHeader(h);err!=nil { return nil,err }
			if _,err:=t.Write(f.data);err!=nil { return nil,err }
		}
		if err:=t.Close();err!=nil { return nil,err }
		if err:=g.Close();err!=nil { return nil,err }
	}
	return []byte(out.String()),nil
}
func build(root,version,commit,date,goVersion string)error {
	if err:=validIdentity(version,commit,date,goVersion);err!=nil{return err}
	var assets []assetRecord
	for _,target:=range platforms {
		files,err:=archiveContents(root,version,commit,date,goVersion,target.os,target.arch)
		if err!=nil{return err}
		b,err:=makeArchive(files,target.os)
		if err!=nil{return err}
		name:=archiveName(version,target.os,target.arch)
		if err:=writeAtomic(filepath.Join(root,name),b);err!=nil{return err}
		assets=append(assets,assetRecord{name,hash(b),int64(len(b))})
		for _,e:=range executables {
			name=assetName(e,version,target.os,target.arch)
			data,err:=readRegular(filepath.Join(root,name),maxExecutableSize)
			if err!=nil{return err}
			assets=append(assets,assetRecord{name,hash(data),int64(len(data))})
		}
	}
	sort.Slice(assets,func(i,j int)bool{return assets[i].Name<assets[j].Name})
	m:=releaseRecord{"2",version,commit,date,goVersion,false,assets}
	b,err:=jsonFile(m)
	if err!=nil{return err}
	if err:=writeAtomic(filepath.Join(root,"RELEASE-METADATA.json"),b);err!=nil{return err}
	var sums strings.Builder
	for _,asset:=range assets { fmt.Fprintf(&sums,"%s  %s
",asset.SHA256,asset.Name) }
	if err:=writeAtomic(filepath.Join(root,"SHA256SUMS"),[]byte(sums.String()));err!=nil{return err}
	return verify(root)
}
func strictJSON(b []byte,v any)error {
	d:=json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err:=d.Decode(v);err!=nil{return err}
	var extra any
	if err:=d.Decode(&extra);!errors.Is(err,io.EOF) {return fmt.Errorf("unexpected trailing JSON")}
	return nil
}
func verify(root string)error {
	top,err:=readRegular(filepath.Join(root,"RELEASE-METADATA.json"),1<<20)
	if err!=nil{return err}
	var m releaseRecord
	if err:=strictJSON(top,&m);err!=nil{return err}
	if m.SchemaVersion!="2" || m.CGOEnabled {return fmt.Errorf("unsupported release metadata")}
	if err:=validIdentity(m.Version,m.Commit,m.BuildDate,m.GoVersion);err!=nil{return err}
	if v:=os.Getenv("VERSION");v!="" && v!=m.Version{return fmt.Errorf("release VERSION mismatch")}
	if c:=os.Getenv("COMMIT");c!="" && c!=m.Commit{return fmt.Errorf("release COMMIT mismatch")}
	if len(m.Assets)!=len(platforms)*(len(executables)+1) {return fmt.Errorf("invalid asset inventory size: %d",len(m.Assets))}
	expected:=make(map[string]struct{},len(m.Assets))
	for _,target:=range platforms {
		expected[archiveName(m.Version,target.os,target.arch)]=struct{}{}
		for _,e:=range executables { expected[assetName(e,m.Version,target.os,target.arch)]=struct{}{} }
	}
	sums,err:=readRegular(filepath.Join(root,"SHA256SUMS"),1<<20)
	if err!=nil{return err}
	var expectedSums strings.Builder
	previous:=""
	for _,a:=range m.Assets {
		if a.Name<=previous {return fmt.Errorf("unsorted or duplicate asset %q",a.Name)}
		previous=a.Name
		if _,ok:=expected[a.Name];!ok {return fmt.Errorf("unexpected asset %q",a.Name)}
		delete(expected,a.Name)
		data,err:=readRegular(filepath.Join(root,a.Name),512<<20)
		if err!=nil{return fmt.Errorf("missing asset %s: %w",a.Name,err)}
		if a.Size!=int64(len(data)) || a.SHA256!=hash(data) {return fmt.Errorf("asset integrity mismatch: %s",a.Name)}
		fmt.Fprintf(&expectedSums,"%s  %s
",a.SHA256,a.Name)
	}
	if len(expected)!=0{return fmt.Errorf("missing expected assets")}
	if string(sums)!=expectedSums.String(){return fmt.Errorf("checksum manifest differs from release metadata")}
	for _,target:=range platforms {
		archive:=archiveName(m.Version,target.os,target.arch)
		data,err:=readRegular(filepath.Join(root,archive),512<<20)
		if err!=nil{return err}
		want,err:=archiveContents(root,m.Version,m.Commit,m.BuildDate,m.GoVersion,target.os,target.arch)
		if err!=nil{return err}
		if err:=verifyArchive(data,target.os,want);err!=nil{return fmt.Errorf("%s: %w",archive,err)}
	}
	dir,err:=os.ReadDir(root)
	if err!=nil{return err}
	for _,e:=range dir {
		if e.Name()=="RELEASE-METADATA.json" || e.Name()=="SHA256SUMS"{continue}
		if _,ok:=knownAsset(m.Assets,e.Name());!ok {return fmt.Errorf("unexpected release file %s",e.Name())}
	}
	return nil
}
func knownAsset(assets []assetRecord,name string)(assetRecord,bool){
	for _,a:=range assets {if a.Name==name{return a,true}}
	return assetRecord{},false
}
func verifyArchive(b []byte,osName string,want []packedFile)error{
	if osName=="windows" {
		z,err:=zip.NewReader(strings.NewReader(string(b)),int64(len(b)))
		if err!=nil{return err}
		if len(z.File)!=len(want){return fmt.Errorf("unexpected ZIP entry count")}
		for i,f:=range z.File {
			if f.Name!=want[i].name || !f.Mode().IsRegular() || f.Mode().Perm()!=os.FileMode(want[i].mode) {
				return fmt.Errorf("unsafe ZIP entry %q",f.Name)
			}
			if f.UncompressedSize64!=uint64(len(want[i].data)){return fmt.Errorf("ZIP entry size mismatch %q",f.Name)}
			r,err:=f.Open()
			if err!=nil{return err}
			data,readErr:=io.ReadAll(io.LimitReader(r,int64(len(want[i].data))+1))
			closeErr:=r.Close()
			if readErr!=nil{return readErr};if closeErr!=nil{return closeErr}
			if hash(data)!=hash(want[i].data) || len(data)!=len(want[i].data) {return fmt.Errorf("ZIP entry bytes mismatch %q",f.Name)}
		}
		return nil
	}
	g,err:=gzip.NewReader(strings.NewReader(string(b)))
	if err!=nil{return err}
	defer g.Close()
	t:=tar.NewReader(g)
	for _,expected:=range want {
		h,err:=t.Next()
		if err!=nil{return fmt.Errorf("missing TAR entry %s: %w",expected.name,err)}
		if h.Name!=expected.name || h.Typeflag!=tar.TypeReg || h.Mode!=expected.mode || h.Size!=int64(len(expected.data)) {
			return fmt.Errorf("unsafe TAR entry %q",h.Name)
		}
		data,err:=io.ReadAll(io.LimitReader(t,int64(len(expected.data))+1))
		if err!=nil{return err}
		if len(data)!=len(expected.data) || hash(data)!=hash(expected.data) {return fmt.Errorf("TAR entry bytes mismatch %q",h.Name)}
	}
	if _,err:=t.Next();!errors.Is(err,io.EOF){return fmt.Errorf("unexpected trailing TAR entry")}
	return nil
}
