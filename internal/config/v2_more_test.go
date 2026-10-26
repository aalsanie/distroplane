package config

import (
 "bytes"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestV2AdditionalCoreAndProviderCases(t *testing.T){
 npm := `{"schemaVersion":"2","release":{"id":"v2","artifacts":{"app":{"source":"dist/app"}}},"destinations":{"npm":{"artifact":"app","options":{"packagePath":"dist/app","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"publish"}}}}}`
 changed:=func(from,to string)string{return strings.Replace(npm,from,to,1)}
 cases:=[]struct{name,raw,want string}{
  {"empty destinations",changed(`"destinations":{"npm":{"artifact":"app","options":{"packagePath":"dist/app","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"publish"}}}}`,`"destinations":{}`),"destinations"},
  {"bad artifact name",changed(`"app":{"source"`,`" app":{"source"`),"release.artifacts"},
  {"bad media type",changed(`"source":"dist/app"`,`"source":"dist/app","mediaType":" invalid"`),"release.artifacts.app.mediaType"},
  {"invalid source whitespace",changed(`"source":"dist/app"`,`"source":" dist/app"`),"release.artifacts.app.source"},
  {"undefined artifact",changed(`"artifact":"app"`,`"artifact":"not-present"`),"destinations.npm.artifact"},
  {"empty artifact",changed(`"artifact":"app"`,`"artifact":""`),"destinations.npm.artifact"},
  {"empty provider array",changed(`"npm":{"artifact":"app","options":{"packagePath":"dist/app","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"publish"}}}`,`"npm":[]`),"destinations.npm"},
  {"invalid executable",changed(`"artifact":"app","options"`,`"artifact":"app","executable":"~/bin/provider","options"`),"destinations.npm.executable"},
  {"unknown options artifact",changed(`"packagePath":"dist/app"`,`"packagePath":"dist/app","artifact":"other"`),"options.artifact"},
  {"invalid optional tag",changed(`"packagePath":"dist/app"`,`"packagePath":"dist/app","tag":" invalid"`),"options.tag"},
  {"invalid JSON type options",changed(`"options":{"packagePath":"dist/app","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"publish"}}`,`"options":false`),"options"},
  {"invalid package path whitespace",changed(`"packagePath":"dist/app"`,`"packagePath":" dist/app"`),"options.packagePath"},
  {"empty npm credential",changed(`"credential":"publish"`,`"credential":""`),"options.authentication.credential"},
  {"URL user info",changed(`"https://registry.npmjs.org/"`,`"https://user:secret@registry.npmjs.org/"`),"options.registry"},
  {"URL scheme",changed(`"https://registry.npmjs.org/"`,`"file:///tmp/registry"`),"options.registry"},
  {"unknown options",changed(`"packagePath":"dist/app"`,`"packagePath":"dist/app","newField":123`),"options.newField"},
 }
 for _,tc:=range cases{
  t.Run(tc.name,func(t *testing.T){
   if tc.raw==npm{t.Fatal("test did not change v2 fixture")}
   _,err:=Decode(strings.NewReader(tc.raw))
   if err==nil{t.Fatalf("invalid config accepted: %s",tc.raw)}
   if !strings.Contains(err.Error(),tc.want){t.Fatalf("expected field %s in %v",tc.want,err)}
   if strings.Contains(err.Error(),"secret@"){t.Fatalf("unsafe URL was echoed: %v",err)}
  })
 }
}

func TestV2CaskAndProviderTypedOptions(t *testing.T){
 raw:=mutateV2Fixture(t,"homebrew-formula.json",func(d,o map[string]any){
  manifest:=o["manifest"].(map[string]any)
  manifest["type"]="cask"
  manifest["name"]="acme"
  delete(manifest,"binary")
  manifest["installKind"]="binary"
  manifest["installSource"]="Acme.app"
 })
 if _,err:=Decode(bytes.NewReader(raw));err!=nil{t.Fatalf("valid cask settings rejected: %v",err)}
 missing:=mutateV2Fixture(t,"homebrew-formula.json",func(d,o map[string]any){
  manifest:=o["manifest"].(map[string]any)
  manifest["type"]="cask"
  delete(manifest,"binary")
  manifest["installKind"]="binary"
 })
 if _,err:=Decode(bytes.NewReader(missing));err==nil||!strings.Contains(err.Error(),"options.manifest.installSource"){
  t.Fatalf("cask missing source not identified: %v",err)
 }
 checksums:=mutateV2Fixture(t,"sdkman-state.json",func(d,o map[string]any){
  o["checksums"]=map[string]string{"SHA-256":strings.Repeat("a",64)}
  o["allowInsecureHTTP"]=false
 })
 if _,err:=Decode(bytes.NewReader(checksums));err!=nil{t.Fatalf("valid SDKMAN checksums rejected: %v",err)}
 for _,tc:=range []struct{name,key,value string}{
  {"bad algorithm key"," bad","valid"},
  {"empty value","SHA-256",""},
 }{
  t.Run(tc.name,func(t *testing.T){
   input:=mutateV2Fixture(t,"sdkman-state.json",func(d,o map[string]any){
    o["checksums"]=map[string]string{tc.key:tc.value}
   })
   if _,err:=Decode(bytes.NewReader(input));err==nil||!strings.Contains(err.Error(),"options.checksums"){
    t.Fatalf("invalid checksum accepted: %v",err)
   }
  })
 }
 insecure:=mutateV2Fixture(t,"npm.json",func(d,o map[string]any){
  o["registry"]="http://registry.invalid/"
  o["allowInsecureHTTP"]=true
 })
 if _,err:=Decode(bytes.NewReader(insecure));err!=nil{t.Fatalf("explicit insecure HTTP setting rejected: %v",err)}
 for _,tc:=range []struct{name,fixture,section,key,value string}{
  {"homebrew homepage","homebrew-formula.json","manifest","homepage","ftp://invalid.example"},
  {"winget installer","winget-msi.json","installer","url","javascript:bad"},
 }{
  t.Run(tc.name,func(t *testing.T){
   input:=mutateV2Fixture(t,tc.fixture,func(d,o map[string]any){
    o[tc.section].(map[string]any)[tc.key]=tc.value
   })
   if _,err:=Decode(bytes.NewReader(input));err==nil||!strings.Contains(err.Error(),"options."+tc.section+"."+tc.key){
    t.Fatalf("invalid nested URL accepted: %v",err)
   }
  })
 }
}

func TestV2LoadPreservesRemoteProviderOptions(t *testing.T){
 raw,err:=os.ReadFile(filepath.Join("testdata","compatibility","sdkman-state.json"))
 if err!=nil{t.Fatal(err)}
 data:=convertLegacyFixture(t,raw)
 dir:=t.TempDir()
 path:=filepath.Join(dir,"config.json")
 if err:=os.WriteFile(path,data,0600);err!=nil{t.Fatal(err)}
 loaded,err:=Load(path)
 if err!=nil{t.Fatal(err)}
 if len(loaded.Config.Targets)!=1||loaded.Config.Targets[0].Provider.Name!="sdkman"{
  t.Fatalf("non-npm options lost: %+v",loaded.Config.Targets)
 }
}

func TestV2PathResolutionRejectsTamperedNormalizedOptions(t *testing.T){
 cfg:=Config{SchemaVersion:SchemaVersionV2,Targets:[]Target{{Provider:Provider{Name:"npm"},Configuration:json.RawMessage(`{}`)}}}
 if err:=resolveV2Paths(&cfg,t.TempDir());err==nil{
  t.Fatal("npm archive path absent from normalized config")
 }
 cfg.Targets[0].Configuration=json.RawMessage(`{"packagePath":false}`)
 if err:=resolveV2Paths(&cfg,t.TempDir());err==nil{
  t.Fatal("invalid normalized npm archive path accepted")
 }
}
