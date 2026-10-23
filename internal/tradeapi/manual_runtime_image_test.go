package tradeapi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Build the exact production measurement/identity source in a disposable clean
// VCS fixture. Minimal domain stubs supply only value types/error/hash helpers;
// the identify and running-image measurement implementations are not retyped.
// No trade API, credential, provider, signer or execution path is composed.
func TestHTTPM4B22RunningImageReplacementSameRevision(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("unsupported production platforms fail closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dir := t.TempDir()
	for _, name := range []string{"manual_runtime_identity.go", "manual_runtime_image.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Replace(string(raw), "package tradeapi", "package main", 1)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"go.mod":  "module runtime-image-fixture\n\ngo 1.26.0\n",
		"main.go": runtimeImageProbe,
	}
	for name, raw := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		// Ignore host Git worktree/config identity. This is an isolated test repo,
		// with no remote and no signing/hooks, never a real repository commit.
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "GIT_") {
				cmd.Env = append(cmd.Env, v)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	run("git", "init", "-q")
	run("git", "add", ".")
	run("git", "-c", "user.name=Runtime Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "fixture")
	run("go", "build", "-buildvcs=true", "-o", filepath.Join(dir, "probe"), ".")
	out := run(filepath.Join(dir, "probe"))
	if !strings.Contains(out, "PINNED_RUNNING_IMAGE_PASS") {
		t.Fatal("probe did not prove exact image", out)
	}
	t.Log(strings.TrimSpace(out))
}

const runtimeImageProbe = `package main
import (
 "context"
 "crypto/sha256"
 "debug/buildinfo"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "os"
 "runtime/debug"
)
type manualAdmissionScope struct {
 AuthorizationID, ManualPolicyID string
 AuthorizationEpoch uint64
 Environment, BuildSHA, ReleaseID, DeploymentID string
}
type fixtureBlock struct { Number uint64; Hash string }
type manualRuntimeProcess struct {
 scope manualAdmissionScope
 binaryHash, manifestHash, networkProfile, holder, provider string
 leaseEpoch int64
 networkCheckpoint fixtureBlock
 dirty bool
}
var ErrInvalid=errors.New("invalid")
func rejectManualInput(s string)error{return errors.New(s)}
func encode(v any)string{b,e:=json.Marshal(v);must(e);return string(b)}
func hash(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])}
func manualHex(s string,lengths ...int)bool{for _,n:=range lengths{if len(s)==n{_,e:=hex.DecodeString(s);return e==nil}};return false}
func must(e error){if e!=nil{panic(e)}}
func revision(b *debug.BuildInfo)string{r,m:="","";for _,s:=range b.Settings{if s.Key=="vcs.revision"{r=s.Value};if s.Key=="vcs.modified"{m=s.Value}};if r==""||m!="false"{panic("not clean VCS metadata")};return r}
func main(){
 ctx:=context.Background()
 path,e:=os.Executable();must(e)
 original,e:=os.ReadFile("/proc/self/exe");must(e)
 info,ok:=debug.ReadBuildInfo();if !ok{panic("no metadata")};rev:=revision(info)
 p:=manualRuntimeProcess{scope:manualAdmissionScope{BuildSHA:rev},binaryHash:hash(original)}
 p.manifestHash=runtimeManifestDigest(p)
 got,e:= (manualMeasuredRuntimeIdentity{manifest:p}).identify(ctx);must(e);if got!=p{panic("baseline image mismatch")}
 must(os.Rename(path,path+".running"))
 replacement:=append(append([]byte(nil),original...),byte(0x73))
 must(os.WriteFile(path,replacement,0700))
 other,e:=buildinfo.ReadFile(path);must(e);if revision(other)!=rev{panic("replacement revision changed")}
 expected:=p;expected.binaryHash=hash(replacement);expected.manifestHash=runtimeManifestDigest(expected)
 got,e=(manualMeasuredRuntimeIdentity{manifest:expected}).identify(ctx)
 if e==nil||got!=(manualRuntimeProcess{}){panic("accepted pathname replacement manifest")}
 got,e=(manualMeasuredRuntimeIdentity{manifest:p}).identify(ctx);must(e);if got!=p{panic("lost original running image after path replacement")}
 must(os.Remove(path))
 got,e=(manualMeasuredRuntimeIdentity{manifest:p}).identify(ctx);must(e);if got!=p{panic("lost image after pathname removal")}
 must(os.Remove(path+".running"))
 // Linux updates the proc link on rename. After unlink it retains the old
 // pathname with " (deleted)"; os.Executable strips that suffix. Reuse that
 // exact reported pathname, rather than assuming rename caches the old name.
 reported,e:=os.Executable();must(e);if reported!=path+".running"{panic("unexpected deleted image pathname")}
 must(os.WriteFile(reported,replacement,0700))
 other,e=buildinfo.ReadFile(reported);must(e);if revision(other)!=rev{panic("reused pathname revision changed")}
 got,e=(manualMeasuredRuntimeIdentity{manifest:expected}).identify(ctx)
 if e==nil||got!=(manualRuntimeProcess{}){panic("accepted reused deleted-image pathname manifest")}
 got,e=(manualMeasuredRuntimeIdentity{manifest:p}).identify(ctx);must(e);if got!=p{panic("lost unlinked running inode")}
 must(os.Remove(reported))
 got,e=(manualMeasuredRuntimeIdentity{manifest:p}).identify(ctx);must(e);if got!=p{panic("lost image after replacement unlink")}
 cancelled,cancel:=context.WithCancel(ctx);cancel();if s,e:=manualRunningImageHash(cancelled);e==nil||s!=""{panic("accepted cancelled measurement")}
 if s,e:=manualRunningImageHash(nil);e==nil||s!=""{panic("accepted nil context")}
 fmt.Println("PINNED_RUNNING_IMAGE_PASS same_clean_revision="+rev+" replacement_rejected original_retained_after_rename_replace_unlink_path_reuse")
}
`
