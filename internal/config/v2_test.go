package config

import (
 "bytes"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "reflect"
 "strings"
 "testing"
)

func convertLegacyFixture(t *testing.T, raw []byte) []byte {
 t.Helper()
 var legacy struct {
  SchemaVersion string `json:"schemaVersion"`
  Release Release `json:"release"`
  Targets []Target `json:"targets"`
 }
 if err:=json.Unmarshal(raw,&legacy);err!=nil{t.Fatal(err)}
 if len(legacy.Targets)!=1{t.Fatalf("expected one target: %d",len(legacy.Targets))}
 artifactMap:=map[string]Artifact{}
 for _,a:=range legacy.Release.Artifacts{artifactMap[a.Name]=a}
 target:=legacy.Targets[0]
 var options map[string]json.RawMessage
 if err:=json.Unmarshal(target.Configuration,&options);err!=nil{t.Fatal(err)}
 var artifact string
 if err:=json.Unmarshal(options["artifact"],&artifact);err!=nil{t.Fatal(err)}
 delete(options,"artifact")
 obj:=map[string]any{
  "schemaVersion":"2",
  "release":map[string]any{"id":legacy.Release.ID,"artifacts":artifactMap},
  "destinations":map[string]any{target.Provider.Name:map[string]any{
   "id":target.ID,"artifact":artifact,"executable":target.Provider.Executable,"options":options,
  }},
 }
 // Artifact v1 has a name field that does not belong in the v2 artifact map.
 for key,val:=range artifactMap{
  obj["release"].(map[string]any)["artifacts"].(map[string]Artifact)[key]=val
 }
 v2Arts:=map[string]map[string]any{}
 for k,a:=range artifactMap{v2Arts[k]=map[string]any{"source":a.Source};if a.MediaType!=""{v2Arts[k]["mediaType"]=a.MediaType}}
 obj["release"].(map[string]any)["artifacts"]=v2Arts
 b,err:=json.Marshal(obj)
 if err!=nil{t.Fatal(err)}
 return b
}

func TestV2NormalizesAllOfficialProviderFixtures(t *testing.T){
 files,err:=filepath.Glob(filepath.Join("testdata","compatibility","*.json"))
 if err!=nil||len(files)!=7{t.Fatalf("v1 fixtures: %v %d",err,len(files))}
 for _,file:=range files{
  t.Run(filepath.Base(file),func(t *testing.T){
   raw,err:=os.ReadFile(file);if err!=nil{t.Fatal(err)}
   legacy,err:=Decode(bytes.NewReader(raw));if err!=nil{t.Fatal(err)}
   v2:=convertLegacyFixture(t,raw)
   modern,err:=Decode(bytes.NewReader(v2));if err!=nil{t.Fatalf("v2 conversion rejected: %v\n%s",err,v2)}
   if modern.SchemaVersion!=SchemaVersionV2{t.Fatalf("schema=%s",modern.SchemaVersion)}
   if !reflect.DeepEqual(legacy.Release,modern.Release)||!reflect.DeepEqual(legacy.Targets,modern.Targets){
    t.Fatalf("normalization differs\nlegacy=%+v\nmodern=%+v",legacy,modern)
   }
   var obj map[string]json.RawMessage
   if err:=json.Unmarshal(v2,&obj);err!=nil{t.Fatal(err)}
   swapped:=fmt.Sprintf(`{"destinations":%s,"release":%s,"schemaVersion":"2"}`,obj["destinations"],obj["release"])
   alternate,err:=Decode(strings.NewReader(swapped))
   if err!=nil||!reflect.DeepEqual(modern,alternate){t.Fatalf("ordering changed normalized intent: %v",err)}
  })
 }
}

func TestV2MultipleDestinationsUseSameArtifact(t *testing.T){
 original:=`{"schemaVersion":"2","release":{"id":"release","artifacts":{"app":{"source":"dist/app"}}},"destinations":{"npm":[{"id":"main","artifact":"app","options":{"packagePath":"dist/app","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}},{"id":"secondary","artifact":"app","options":{"packagePath":"dist/app","registry":"https://secondary.example/","authentication":{"mode":"token","credential":"npm-publish"}}}]}}`
 cfg,err:=Decode(strings.NewReader(original))
 if err!=nil{t.Fatal(err)}
 if len(cfg.Targets)!=2||cfg.Targets[0].ID!="main"||cfg.Targets[1].ID!="secondary"{
  t.Fatalf("unrecognized destinations: %+v",cfg.Targets)
 }
}

func TestV2RejectsMalformedAndAmbiguousInputs(t *testing.T){
 base:=`{"schemaVersion":"2","release":{"id":"v2","artifacts":{"app":{"source":"dist/package.tgz"}}},"destinations":{"npm":{"artifact":"app","options":{"packagePath":"dist/package.tgz","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}}}`
 replace:=func(old,new string)string{return strings.Replace(base,old,new,1)}
 cases:=map[string]struct{config,field string}{
  "unknown core":{replace(`"schemaVersion":"2"`,`"schemaVersion":"2","unknown":"field"`),"unknown"},
  "missing release identity":{replace(`"id":"v2"`,`"id":""`),"release.id"},
  "no artifact":{replace(`"app":{"source":"dist/package.tgz"}`,``),"release.artifacts"},
  "unknown artifact":{replace(`"artifact":"app"`,`"artifact":"missing"`),"artifact"},
  "unknown destination provider":{replace(`"npm":{`,`"other":{`),"destinations.other"},
  "duplicate destination":{replace(`"npm":{"artifact":"app"`,`"npm":[{"id":"same","artifact":"app"`),"destinations.npm"},
  "bad destination field":{replace(`"artifact":"app","options"`,`"artifact":"app","unexpected":true,"options"`),"destinations.npm"},
  "unknown provider field":{replace(`"packagePath":"dist/package.tgz"`,`"packagePath":"dist/package.tgz","password":"supersecret"`),"options.password"},
  "missing metadata":{replace(`"registry":"https://registry.npmjs.org/",`,``),"options.registry"},
  "bad registry":{replace(`"https://registry.npmjs.org/"`,`"ftp://registry.example"`),"options.registry"},
  "inline credentials":{replace(`"credential":"npm-publish"`,`"credential":"npm-publish","token":"SECRET_VALUE"`),"authentication.token"},
  "interpolation":{replace(`"source":"dist/package.tgz"`,`"source":"${PWD}/package.tgz"`),"release.artifacts.app.source"},
  "null options":{replace(`"options":{"packagePath"`,`"options":null,"packagePath"`),"options"},
  "duplicate key":{replace(`"schemaVersion":"2"`,`"schemaVersion":"2","schemaVersion":"2"`),"decode config"},
  "trailing data":{base+"{}", "decode config"},
 }
 for name,tc:=range cases{
  t.Run(name,func(t *testing.T){
   _,err:=Decode(strings.NewReader(tc.config))
   if err==nil{t.Fatal("invalid v2 accepted")}
   if !strings.Contains(err.Error(),tc.field){
    t.Fatalf("missing diagnostic field %q: %v",tc.field,err)
   }
   if strings.Contains(err.Error(),"supersecret")||strings.Contains(err.Error(),"SECRET_VALUE"){
    t.Fatalf("unsafe value leaked in validation error: %v",err)
   }
  })
 }
}

func TestV2LoadResolvesRelativeNpmPackagePath(t *testing.T){
 root:=t.TempDir()
 config:=`{"schemaVersion":"2","release":{"id":"v2","artifacts":{"app":{"source":"dist/app.tgz"}}},"destinations":{"npm":{"artifact":"app","options":{"packagePath":"dist/app.tgz","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}}}`
 path:=filepath.Join(root,"config.json")
 if err:=os.WriteFile(path,[]byte(config),0600);err!=nil{t.Fatal(err)}
 loaded,err:=Load(path)
 if err!=nil{t.Fatal(err)}
 var options struct{PackagePath string `json:"packagePath"`}
 if err:=json.Unmarshal(loaded.Config.Targets[0].Configuration,&options);err!=nil{t.Fatal(err)}
 expected:=filepath.Join(root,"dist","app.tgz")
 if options.PackagePath!=expected{t.Fatalf("packagePath=%q expected %q",options.PackagePath,expected)}
}
