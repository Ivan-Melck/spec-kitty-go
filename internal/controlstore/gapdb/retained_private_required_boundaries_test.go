package gapdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

// Family 11 only: source-valid in-memory readback and the existing simulated
// Unix transport. NOT native Host/provider admission, expiry, or history proof.
// The Go overlay ADDS a test in the owner package, reusing its existing private
// fixture/functions without exporting a factory, copying production functions,
// editing another repository file, or replacing any production source.
func TestRetainedPrivateRequiredOwnerBoundaries(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(here), "../../.."))
	for _, path := range []string{
		"internal/productionruntime/retained_private_records.go",
		"internal/productionruntime/retained_private_records_test.go",
		"internal/productionruntime/recovery.go",
		"internal/productionruntime/expiry_qualification.go",
		"internal/productionruntime/expiry_qualification_binding_test.go",
		"internal/controljournal/v2/model.go",
	} {
		raw, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("source %s sha256=%x", path, sha256.Sum256(raw))
	}
	// Explicit child modes ensure the PRIVATE tests themselves, not just this
	// launcher, run both unit/race and ordinary/expiry qualification profiles.
	for _, profile := range []string{"ordinary", "expiry"} {
		for _, race := range []bool{false, true} {
			name := profile + "/unit"
			if race {
				name = profile + "/race"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				source := retainedPrivateRequiredOwnerSource
				if profile == "expiry" {
					source += retainedPrivateRequiredExpirySource
				}
				file := filepath.Join(dir, "required_test.go")
				if err := os.WriteFile(file, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				virtual := filepath.Join(root, "internal/productionruntime/zz_retained_private_required_boundaries_test.go")
				raw, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{virtual: file}})
				if err != nil {
					t.Fatal(err)
				}
				overlay := filepath.Join(dir, "overlay.json")
				if err := os.WriteFile(overlay, raw, 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
				defer cancel()
				args := []string{"test", "-overlay=" + overlay, "-count=1", "-timeout=90s", "-v", "-run=^TestRetainedPrivateRequired", "./internal/productionruntime"}
				if profile == "expiry" {
					args = append(args, "-tags=spec_kitty_expiry_host_qualification")
				}
				if race {
					args = append(args, "-race")
				}
				cmd := exec.CommandContext(ctx, "go", args...)
				cmd.Dir = root
				for _, entry := range os.Environ() {
					key := strings.SplitN(entry, "=", 2)[0]
					if !strings.HasPrefix(key, "SPEC_KITTY_") && key != "GOTOOLCHAIN" && key != "GOFLAGS" && key != "GOEXPERIMENT" {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				cmd.Env = append(cmd.Env, "GOTOOLCHAIN=go1.26.7", "GOFLAGS=", "GOEXPERIMENT=")
				output, err := cmd.CombinedOutput()
				t.Logf("%s\n%s", strings.Join(args, " "), output)
				if err != nil {
					t.Fatalf("first owner-test outcome (no product repair): %v", err)
				}
			})
		}
	}
}

// A literal 1MiB+1 FRAME is below the ACTUAL 12MiB transport bound, not a
// private-helper RPC-budget rejection. Send valid complete JSON rather than
// letting malformed JSON counterfeit a budget failure. Padding is JSON space,
// not unknown fields; the small value remains legal and is actually decoded.
func TestRetainedPrivateRequiredUnixLiteralResponse(t *testing.T) {
	const target = 1048577
	lengths := make(chan int, 2)
	transport, _, accepted := retainedRequiredFixture(t, func(_ *net.UnixConn, request retainedRequest) []byte {
		raw := retainedSuccess(t, request, map[string]any{"record": gapdbapi.Record{Key: "k", Value: []byte("v"), Revision: 1}})
		if request.RequestID != "owned-read-1" {
			if len(raw) > target {
				t.Error("fixture response exceeds target")
			} else {
				raw = append(bytes.Repeat([]byte(" "), target-len(raw)), raw...)
			}
		}
		lengths <- len(raw)
		return raw
	})
	if record, err := transport.get(t.Context(), "k"); err != nil || string(record.Value) != "v" {
		t.Fatal("fresh positive prerequisite", record, err)
	}
	if first := <-lengths; first >= target {
		t.Fatal("positive prerequisite was not a small response")
	}
	if target >= MaxWireFrameBytes || MaxWireFrameBytes != 12<<20 {
		t.Fatal("transport bound drift")
	}
	record, err := transport.get(t.Context(), "k")
	if err != nil || record.Key != "k" || string(record.Value) != "v" || accepted.Load() != 2 {
		t.Fatal("literal legal 1048577-byte frame did not reach decoder success", err)
	}
	if actual := <-lengths; actual != target {
		t.Fatal("not the literal response size", actual)
	}
	t.Log("1048577 complete response bytes: existing Unix reader + decoder succeeded; NOT private 1MiB-budget refusal")
}

func TestRetainedPrivateRequiredUnixActualFrameGuard(t *testing.T) {
	transport, _, accepted := retainedRequiredFixture(t, func(conn *net.UnixConn, request retainedRequest) []byte {
		if request.RequestID == "owned-read-1" {
			return retainedSuccess(t, request, map[string]any{"record": gapdbapi.Record{Key: "k", Value: []byte("v"), Revision: 1}})
		}
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], MaxWireFrameBytes+1)
		_, _ = conn.Write(header[:]) // No body: header guard must precede body/JSON.
		return nil
	})
	if _, err := transport.get(t.Context(), "k"); err != nil {
		t.Fatal("fresh positive prerequisite", err)
	}
	record, err := transport.get(t.Context(), "k")
	var failed *gapdbapi.TransportError
	if !reflect.ValueOf(record).IsZero() || !errors.As(err, &failed) || failed.Cause == nil || failed.Cause.Error() != "owned response exceeds frame bound" || errors.Is(err, errRetainedJSON) || accepted.Load() != 2 {
		t.Fatalf("wrong stage/cause: record=%+v err=%v", record, err)
	}
	transport.mu.Lock()
	active := retainedRegistryCountLocked(t, transport)
	transport.mu.Unlock()
	if active != 0 {
		t.Fatal("frame failure leaked connection")
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	before := accepted.Load()
	if record, err := transport.get(t.Context(), "k"); err == nil || !reflect.ValueOf(record).IsZero() || accepted.Load() != before {
		t.Fatal("closed transport renewed work")
	}
	t.Log("actual 12582913-byte declared frame refused before body allocation/decoder; not a 1048577-byte transport cap")
}

const retainedPrivateRequiredOwnerSource = `package productionruntime

import (
 "bytes"
 "context"
 "encoding/json"
 "fmt"
 "math"
 "os"
 "reflect"
 "runtime"
 "sort"
 "strings"
 "testing"
 "time"
 journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
 storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

func requiredCaps(t *testing.T, p retainedPrivatePolicy) {
 t.Helper()
 if runtime.Version() != "go1.26.7" || p != defaultRetainedPrivatePolicy() ||
 p.maximumEpochs != 64 || p.maximumRows != 195 || p.maximumValueBytes != 4096 ||
 p.maximumTotalBytes != 262144 || p.maximumRPCBytes != 1048576 || p.maximumDuration != 5*time.Second {
  t.Fatal("normative caps/toolchain changed", runtime.Version(), p)
 }
}
func requiredPositive(t *testing.T, f *retainedPrivateFixture) []string {
 t.Helper()
 p := defaultRetainedPrivatePolicy(); requiredCaps(t,p)
 result, err := f.observe(context.Background(),p)
 if err != nil || len(result.rows) != len(f.reader.rows) || len(f.reader.trace) != len(result.rows) { t.Fatal("fresh positive prerequisite",err) }
 trace := append([]string(nil),f.reader.trace...); f.reader.trace=nil
 return trace
}
func requiredRefusedAt(t *testing.T, f *retainedPrivateFixture, p retainedPrivatePolicy, trace []string, key string) {
 t.Helper()
 result,err := f.observe(context.Background(),p)
 retainedPrivateAssertRefused(t,result,err)
 if !reflect.ValueOf(result).IsZero() { t.Fatal("nonzero partial result") }
 stop:=-1
 for i,k:=range trace { if k==key { stop=i; break } }
 if stop<0 || !reflect.DeepEqual(f.reader.trace,trace[:stop+1]) { t.Fatalf("wrong read stage: got %v target %s baseline %v", f.reader.trace,key,trace) }
 requiredCaps(t,p)
 // The full exact suffix must remain unvisited; no continuation/new attempt
 // is issued by the failed observation. This stateless helper is NOT a lease.
}
func requiredPaths(t *testing.T, raw []byte) [][]string {
 t.Helper()
 var obj map[string]json.RawMessage
 if err:=json.Unmarshal(raw,&obj);err!=nil {t.Fatal(err)}
 names:=make([]string,0,len(obj));for k:=range obj { names=append(names,k) };sort.Strings(names)
 var out [][]string
 for _,k:=range names {
  out=append(out,[]string{k})
  if len(obj[k])>0 && obj[k][0]=='{' {for _,p:=range requiredPaths(t,obj[k]) {out=append(out,append([]string{k},p...))}}
 }
 return out
}
func requiredChange(t *testing.T, raw []byte, path []string, replacement json.RawMessage) []byte {
 t.Helper()
 var obj map[string]json.RawMessage
 if err:=json.Unmarshal(raw,&obj);err!=nil {t.Fatal(err)}
 if len(path)>1 {obj[path[0]]=requiredChange(t,obj[path[0]],path[1:],replacement)} else if replacement==nil {delete(obj,path[0])} else {obj[path[0]]=replacement}
 return retainedPrivateSourceCanonical(t,obj)
}
func requiredMember(t *testing.T, raw []byte, path []string) json.RawMessage {
 t.Helper()
 var obj map[string]json.RawMessage
 if err:=json.Unmarshal(raw,&obj);err!=nil {t.Fatal(err)}
 if len(path)==1 {return obj[path[0]]};return requiredMember(t,obj[path[0]],path[1:])
}
func requiredKind(raw []byte) byte {
 switch raw[0] {case '"':return 's';case '{':return 'o';case '[':return 'a';case 't','f':return 'b';case 'n':return 'z';default:return 'n'}
}
func TestRetainedPrivateRequiredEveryClosedMemberWrongType(t *testing.T) {
 base:=newRetainedPrivateFixture(t,2)
 keys:=requiredPositive(t,&base)
 members,cases:=0,0
 for _,key:=range keys {
  for _,path:=range requiredPaths(t,base.reader.rows[key].Value) {
   members++
   original:=requiredMember(t,base.reader.rows[key].Value,path)
   for _,bad:=range []string{"[]","{}","true","7","\"wrong\""} {
    if requiredKind([]byte(bad))==requiredKind(original) {continue}
    cases++
    t.Run(key+"/"+strings.Join(path,".")+"/"+bad,func(t *testing.T){
     f:=newRetainedPrivateFixture(t,2);trace:=requiredPositive(t,&f)
     row:=f.reader.rows[key];row.Value=requiredChange(t,row.Value,path,json.RawMessage(bad));f.reader.rows[key]=row
     requiredRefusedAt(t,&f,defaultRetainedPrivatePolicy(),trace,key)
    })
   }
  }
 }
 // Includes wrapper.current itself, all nested epoch members, and the present
 // predecessor in current/latest epoch; the oldest predecessor is omitted.
 if members!=60 || cases!=240 {t.Fatalf("closed matrix drift: members=%d cases=%d",members,cases)}
 t.Logf("%d closed present members, %d incompatible JSON-type mutations, exact failed-read prefixes",members,cases)
}

func TestRetainedPrivateRequiredLiteralRPCValue(t *testing.T) {
 f:=newRetainedPrivateFixture(t,1);trace:=requiredPositive(t,&f)
 key:=trace[0];row:=f.reader.rows[key];row.Value=bytes.Repeat([]byte("x"),1048577);f.reader.rows[key]=row
 p:=defaultRetainedPrivatePolicy();requiredRefusedAt(t,&f,p,trace,key)
 // Direct actual read distinguishes envelope/size guard from RPC precharge:
 // Get executed, worst ordinary response stayed charged, no accepted bytes.
 f.reader.trace=nil
 w:=retainedPrivateWork{ctx:context.Background(),reader:f.reader,policy:p,capturedRevision:f.captured.CurrentRevision(),rows:make(map[string]retainedPrivateRecord)}
 got,err:=w.read(key)
 if err==nil || !reflect.ValueOf(got).IsZero() || len(f.reader.trace)!=1 || w.totalBytes!=0 || len(w.rows)!=0 || w.rpcBytes!=storegapdb.MaxOrdinaryValueBytes {t.Fatal("wrong literal-response rejection stage",err,w)}
 requiredCaps(t,p)
 t.Log("1048577 value bytes actually returned by in-memory Get: PRIVATE 4096-byte guard, NOT RPC-budget branch; no refund or partial result")
}

func TestRetainedPrivateRequiredActualFiveSecondDeadline(t *testing.T) {
 f:=newRetainedPrivateFixture(t,1);trace:=requiredPositive(t,&f)
 p:=defaultRetainedPrivatePolicy();var deadline time.Time;var entered time.Time
 f.reader.hook=func(ctx context.Context,_ string)(storegapdb.Record,error,bool){
  entered=time.Now();var ok bool;deadline,ok=ctx.Deadline();if !ok {t.Fatal("missing real deadline")}
  <-ctx.Done()
  return storegapdb.Record{},fmt.Errorf("SECRET provider wrapped: %w",ctx.Err()),true
 }
 start:=time.Now();result,err:=f.observe(context.Background(),p);end:=time.Now()
 retainedPrivateAssertRefused(t,result,err)
 if !reflect.ValueOf(result).IsZero() || err!=context.DeadlineExceeded || end.Sub(start)<5*time.Second || end.Before(deadline) || deadline.Before(start.Add(5*time.Second)) || entered.Before(start) || !reflect.DeepEqual(f.reader.trace,trace[:1]) {t.Fatal("not actual fixed-five-second cancellation",end.Sub(start),err)}
 requiredCaps(t,p)
 t.Logf("real default context elapsed=%s; deadline reached, Get cooperated, no subsequent read; NOT hard-wall SLO",end.Sub(start))
}

func TestRetainedPrivateRequiredLiteralAggregateUnreachable(t *testing.T) {
 f:=newRetainedPrivateFixture(t,64)
 f.binding.HostAuthorityID=journalv2.HostAuthorityID(strings.Repeat("h",128));f.binding.DatabaseID=journalv2.DatabaseID(strings.Repeat("d",128));f.binding.CreatedOperationID=strings.Repeat("o",128)
 // Ordinary wire/storage uint64s have no upper semantic restriction beyond
 // nonzero. Include their FULL 20-digit widths in the source-size proof.
 f.binding.WireProtocol=math.MaxUint64;f.binding.StorageProtocol=math.MaxUint64
 raw,err:=f.binding.canonical();if err!=nil {t.Fatal(err)}
 row:=f.reader.rows[authorityBindingKey];row.Value=raw;f.reader.rows[authorityBindingKey]=row
 for i,c:=range f.current {
  c.HostAuthorityID=f.binding.HostAuthorityID;c.DatabaseID=f.binding.DatabaseID
  c.RecoveredStoreRevision=math.MaxUint64-uint64(2*(64-i))
  f.replaceCurrent(t,i,c)
  for _,key:=range []string{runtimeEpochRecordKey(c.RuntimeEpochID),"spk/runtime-admission/v2/operations/"+c.CreatedOperationID,"spk/runtime-admission/v2/events/"+string(c.RuntimeEpochID)} {r:=f.reader.rows[key];r.Revision=c.RecoveredStoreRevision+1;f.reader.rows[key]=r}
  if i==63 {for _,key:=range []string{runtimeCurrentKey,retainedPrivateHeadKey} {r:=f.reader.rows[key];r.Revision=c.RecoveredStoreRevision+1;f.reader.rows[key]=r}}
 }
 f.captured,err=journalv2.NewStoreRevisionV2(f.binding.DatabaseID,math.MaxUint64,math.MaxUint64);if err!=nil {t.Fatal(err)}
 trace:=requiredPositive(t,&f)
 largest,total:=0,0
 for _,r:=range f.reader.rows {largest=max(largest,len(r.Value));total+=len(r.Value)}
 // All string positions are now at their legal maximum width; generated IDs
 // and digests have fixed widths, ready/resolved/event strings are exact;
 // uint64s are20 decimal digits; oldest omitted predecessor only shortens.
 // Schema1 maximum1061 is >= ALL ordinary serialized row shapes here.
 if len(trace)!=195 || largest>1061 || total>=262144 {t.Fatal("source envelope proof drift",len(trace),largest,total)}
 const acceptedRowUpper=1061
 const prefixUpper=194*acceptedRowUpper
 const finalReadUpper=prefixUpper+4096
 const rpcReservationUpper=195*acceptedRowUpper+storegapdb.MaxOrdinaryValueBytes+2*4096
 if finalReadUpper>=262145 || rpcReservationUpper>=1048576 {t.Fatal("unreachability arithmetic")}
 // Source-bind the guards establishing that a failed row cannot continue,
 // padding/unknown members cannot enlarge an accepted prefix, and all token
 // widths above are really enforced. These are SOURCE obligations, not mocks.
 source,err:=os.ReadFile("retained_private_records.go");if err!=nil {t.Fatal(err)}
 for _,guard:=range []string{
  "size > w.policy.maximumValueBytes", "size > w.policy.maximumTotalBytes-w.totalBytes",
  "len(w.rows) >= w.policy.maximumRows", "len(epochs) >= w.policy.maximumEpochs",
  "!bytes.Equal(bindingRow.value, bindingBytes)", "!bytes.Equal(currentBytes, currentRow.value)",
  "!bytes.Equal(head.value, headBytes)", "!bytes.Equal(epochRow.value, epochBytes)",
  "!bytes.Equal(operation.value, operationBytes)", "!bytes.Equal(event.value, eventBytes)",
  "len(value) != len(prefix)+32", "hexDigest(c.ProjectionSHA256)",
 } {if !strings.Contains(string(source),guard) {t.Fatal("source obligation changed",guard)}}
 t.Logf("max-width legal195-row execution: largest=%d total=%d; source proof: any194-row valid prefix <=%d, plus one4096-byte read <=%d <262145. Literal aggregate guard UNREACHABLE without earlier source/row/value refusal. RPC reserve upper=%d <1048576; private RPC exhaustion likewise unreachable on source-valid prefixes",largest,total,prefixUpper,finalReadUpper,rpcReservationUpper)
}
`

const retainedPrivateRequiredExpirySource = `
func TestRetainedPrivateRequiredExpiryEveryBindingByteMember(t *testing.T) {
 // Reuse the existing private fixture AND existing schema1 binding fixture.
 // Only binding expectations/database/current readback are adjusted, never
 // authority construction, provider state, a clock, or any observation cap.
 apply:=func(t *testing.T,f *retainedPrivateFixture){
  f.binding=expiryTestBinding()
  raw,err:=f.binding.canonical();if err!=nil {t.Fatal(err)}
  row:=f.reader.rows[authorityBindingKey];row.Value=raw;f.reader.rows[authorityBindingKey]=row
  for i,c:=range f.current {c.HostAuthorityID=f.binding.HostAuthorityID;c.DatabaseID=f.binding.DatabaseID;f.replaceCurrent(t,i,c)}
  f.captured,err=journalv2.NewStoreRevisionV2(f.binding.DatabaseID,f.captured.CurrentRevision(),f.captured.DurableThroughRevision());if err!=nil {t.Fatal(err)}
 }
 base:=newRetainedPrivateFixture(t,2);apply(t,&base);requiredPositive(t,&base)
 raw:=base.reader.rows[authorityBindingKey].Value;paths:=requiredPaths(t,raw)
 if len(paths)!=15 {t.Fatal("not closed schema1 binding",len(paths))}
 cases:=0
 for _,path:=range paths {
  original:=requiredMember(t,raw,path)
  changes:=map[string]json.RawMessage{"missing":nil,"null":json.RawMessage("null"),"array":json.RawMessage("[]"),"object":json.RawMessage("{}")}
  switch requiredKind(original) {
  case 's':
   changes["boolean"]=json.RawMessage("true");changes["number"]=json.RawMessage("7")
   changes["empty"]=json.RawMessage("\"\"");changes["wrong_value"]=json.RawMessage("\"SECRET-wrong\"")
  case 'n':
   changes["boolean"]=json.RawMessage("true");changes["string"]=json.RawMessage("\"1\"")
   for _,s:=range []string{"0","2","-1","1.0","18446744073709551616"} {changes["numeric_"+s]=json.RawMessage(s)}
  case 'b':
   changes["number"]=json.RawMessage("0");changes["string"]=json.RawMessage("\"false\"");changes["wrong_value"]=json.RawMessage("true")
  default:t.Fatal("unexpected binding member",path)
  }
  names:=make([]string,0,len(changes));for name:=range changes {names=append(names,name)};sort.Strings(names)
  for _,name:=range names {
   cases++
   t.Run(path[0]+"/"+name,func(t *testing.T){
    f:=newRetainedPrivateFixture(t,2);apply(t,&f);trace:=requiredPositive(t,&f)
    row:=f.reader.rows[authorityBindingKey];row.Value=requiredChange(t,row.Value,path,changes[name]);f.reader.rows[authorityBindingKey]=row
    requiredRefusedAt(t,&f,defaultRetainedPrivatePolicy(),trace,authorityBindingKey)
   })
  }
  // Every byte member additionally rejects duplicate, case alias, changed
  // lexical spelling and invalid JSON, independently of semantic decoding.
  for _,mode:=range []string{"duplicate","case_alias","space","invalid_json"} {
   cases++
   t.Run(path[0]+"/"+mode,func(t *testing.T){
    f:=newRetainedPrivateFixture(t,2);apply(t,&f);trace:=requiredPositive(t,&f)
    row:=f.reader.rows[authorityBindingKey]
    name:=path[0];if mode=="case_alias" {name=strings.ToUpper(name)}
    if mode=="space" {needle:=[]byte(fmt.Sprintf("%q:",name));row.Value=bytes.Replace(row.Value,needle,append(needle,byte(' ')),1)} else if mode=="invalid_json" {needle:=[]byte(fmt.Sprintf("%q:",name));row.Value=bytes.Replace(row.Value,append(bytes.Clone(needle),original...),append(needle,byte('!')),1);if json.Valid(row.Value) {t.Fatal("mutation not malformed JSON")}} else {row.Value=append(append(bytes.Clone(row.Value[:len(row.Value)-1]),[]byte(fmt.Sprintf(",%q:",name))...),append(bytes.Clone(original),byte('}'))...)}
    f.reader.rows[authorityBindingKey]=row
    requiredRefusedAt(t,&f,defaultRetainedPrivatePolicy(),trace,authorityBindingKey)
   })
  }
 }
 if cases!=186 {t.Fatalf("binding matrix drift cases=%d",cases)}
 t.Logf("schema1: all15 byte members; %d mutations; every refusal at first binding Get/equality, no derived lookup",cases)
 // The alternate fixed binding is the largest allowed schema in the common
 // unreachability proof. Exercise its actual source serializer at all maxima.
 b:=expiryTestBinding();b.HostAuthorityID=journalv2.HostAuthorityID(strings.Repeat("h",128));b.DatabaseID=journalv2.DatabaseID(strings.Repeat("d",128));b.CreatedOperationID=strings.Repeat("o",128)
 maximum,err:=b.canonical();if err!=nil || len(maximum)!=1061 || expiryBindingMaximum!=1061 {t.Fatal("schema1 maximum proof drift",len(maximum),err)}
 t.Log("schema1 maximum-token serializer:1061 bytes; common prefix proof covers this larger binding too")
}
`
