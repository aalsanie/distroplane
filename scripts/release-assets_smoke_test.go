package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseArchiveNativeExecutables(t *testing.T) {
	root := os.Getenv("DISTROPLANE_RELEASE_SMOKE_DIR")
	if root == "" { t.Skip("set DISTROPLANE_RELEASE_SMOKE_DIR to test built release") }
	version := os.Getenv("VERSION")
	if version == "" { t.Fatal("VERSION is required to verify release identity") }
	osName, arch := runtime.GOOS, runtime.GOARCH
	archive := archiveName(version, osName, arch)
	b, err := os.ReadFile(filepath.Join(root, archive))
	if err != nil { t.Fatal(err) }
	dest := t.TempDir()
	extract := func(name string, mode os.FileMode, reader io.Reader, size int64) {
		t.Helper()
		if size < 0 || size > maxExecutableSize { t.Fatalf("invalid entry size: %s", name) }
		known := name == "PACKAGE-METADATA.json"
		for _, exe := range executables { known = known || innerName(exe, osName) == name }
		if !known { t.Fatalf("unexpected path: %s", name) }
		path := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path),0755);err!=nil{t.Fatal(err)}
		data, err := io.ReadAll(io.LimitReader(reader, size+1))
		if err!=nil || int64(len(data))!=size {t.Fatalf("read %s: %v",name,err)}
		if err:=os.WriteFile(path,data,mode);err!=nil{t.Fatal(err)}
	}
	if osName=="windows"{
		z,err:=zip.NewReader(strings.NewReader(string(b)),int64(len(b)))
		if err!=nil{t.Fatal(err)}
		for _,f:=range z.File{
			r,err:=f.Open()
			if err!=nil{t.Fatal(err)}
			extract(f.Name,f.Mode().Perm(),r,int64(f.UncompressedSize64))
			if err:=r.Close();err!=nil{t.Fatal(err)}
		}
	}else{
		g,err:=gzip.NewReader(strings.NewReader(string(b)))
		if err!=nil{t.Fatal(err)}
		defer g.Close()
		r:=tar.NewReader(g)
		for {
			h,err:=r.Next()
			if err==io.EOF{break}
			if err!=nil{t.Fatal(err)}
			extract(h.Name,os.FileMode(h.Mode),r,h.Size)
		}
	}
	metadata,err:=os.ReadFile(filepath.Join(dest,"PACKAGE-METADATA.json"))
	if err!=nil{t.Fatal(err)}
	var pkg packageRecord
	if err:=strictJSON(metadata,&pkg);err!=nil{t.Fatal(err)}
	if pkg.Version!=version || pkg.OS!=osName || pkg.Arch!=arch || len(pkg.Files)!=len(executables) {
		t.Fatalf("archive identity does not match: %+v",pkg)
	}
	ext:=""
	if osName=="windows"{ext=".exe"}
	cli:=exec.Command(filepath.Join(dest,"bin","distroplane"+ext),"version","--json")
	out,err:=cli.CombinedOutput()
	if err!=nil{t.Fatalf("CLI did not execute: %v: %s",err,out)}
	var identity struct{Version string `json:"version"`;Commit string `json:"commit"`}
	if err:=json.Unmarshal(out,&identity);err!=nil{t.Fatal(err)}
	if identity.Version!=version || identity.Commit!=pkg.Commit {t.Fatalf("CLI identity mismatch: %+v",identity)}
	for _,name:=range executables[1:]{
		p:=exec.Command(filepath.Join(dest,"libexec",name+ext))
		p.Stdin=strings.NewReader(`{"protocolVersion":"1","requestId":"archive-smoke","operation":"describe","payload":{}}`)
		output,err:=p.CombinedOutput()
		if err!=nil{t.Fatalf("%s did not start: %v: %s",name,err,output)}
		var reply struct{
			Status string `json:"status"`
			ProtocolVersion string `json:"protocolVersion"`
			Payload struct{Provider struct{
				Name string `json:"name"`
				Version string `json:"version"`
			} `json:"provider"`} `json:"payload"`
		}
		if err:=json.Unmarshal(output,&reply);err!=nil{t.Fatalf("%s invalid response: %v: %s",name,err,output)}
		if reply.Status!="ok" || reply.ProtocolVersion!="1" ||
			reply.Payload.Provider.Name!=strings.TrimPrefix(name,"distroplane-provider-") ||
			reply.Payload.Provider.Version!=version {
			t.Fatalf("%s incorrect identity: %s",name,fmt.Sprintf("%+v",reply))
		}
	}
}
