//go:build spec_kitty_expiry_host_qualification && linux && amd64
package productionruntime

import (
 "context"
 "crypto/sha256"
 "fmt"
 "errors"
 "go/ast"
 "go/parser"
 "go/token"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "testing"
)

// Strong typed custody BEFORE any assertion; no t.TempDir, cleanup callback,
// deferred cancel, restoration or manual retirement may release unsafe roots.
var h06LateCustody struct { sync.Mutex; leases []*expiryHostLease }
func h06LateReserve(t *testing.T) *expiryHostLease {
 t.Helper()
 cache,err:=os.UserCacheDir(); if err!=nil { t.Fatal(err) }
 parent,err:=os.MkdirTemp(cache,"h06-late-metadata-"); if err!=nil { t.Fatal(err) }
 t.Log("NEW preserved component parent",parent)
 l,err:=reserveExpiryHostLease(context.Background(),Config{ControlRoot:filepath.Join(parent,"root")})
 if l!=nil { h06LateCustody.Lock();h06LateCustody.leases=append(h06LateCustody.leases,l);h06LateCustody.Unlock() }
 if err!=nil||l==nil { t.Fatal(err) }
 // Explicit construction-state COMPONENT, not a fake healthy Host/gate. The
 // original productionOwner.Close owns a real newly acquired File; rollback
 // itself must retire and complete the real empty generation work window.
 owned,err:=os.OpenFile(filepath.Join(parent,"component-owner"),os.O_RDWR|os.O_CREATE|os.O_EXCL,0600)
 if err!=nil{t.Fatal(err)}
 r:=&expiryHostRun{lease:l,window:newExpiryWorkWindow()}
 r.owner=&productionOwner{expiry:ownerExpiryState{run:r},closer:owned}
 l.control.candidate,l.control.stage=r,expiryControlConstructing
 return l
}
func h06LateCounts(t *testing.T,l *expiryHostLease, residual bool, metadata uint64) {
 t.Helper()
 for _,kind:=range []expiryRootDescriptorKind{expiryRootParent,expiryRootData,expiryRootLock} {
  c:=&l.cleanup.categories[kind]; closed:=uint64(1);if residual { closed=0 }
  if c.acquired.Load()!=1||c.attempted.Load()!=closed||c.completed.Load()!=closed { t.Fatal("named root FIRST census",kind) }
 }
 m:=&l.cleanup.categories[expiryRootMetadata]
 if m.acquired.Load()!=metadata||m.attempted.Load()!=metadata||m.completed.Load()!=metadata { t.Fatal("FOURTH cumulative metadata category") }
}
func h06LateResidual(t *testing.T,l *expiryHostLease,c *expiryLeaseControl,primary,got error,field string) {
 t.Helper()
 if !errors.Is(got,primary)||c.firstErr!=primary||l.control!=c||!l.residualPinned||c.stage!=expiryControlTerminalResidual||l.phase==expiryLeaseClosed { t.Fatal("original primary/canonical strong residual lost",got) }
 first:=l.cleanup.first.Load()
 if first==nil||errors.Is(first.err,primary)||errors.Is(first.err,context.Canceled) {t.Fatal("earlier actual metadata FIRST overwritten by later primary/context")}
 var typed *Error
 if !errors.As(l.cleanup.outcome(false),&typed)||typed.Kind!=ErrorStartup||typed.State!=HostRecovering||typed.Field!=field||!errors.Is(got,typed) { t.Fatal("actual metadata classification/union lost",got) }
 if !errors.Is(first.err,typed) {t.Fatal("metadata FIRST is not the actual typed refusal")}
 h06LateOriginalOwnerDrained(t,l,c)
 for _,f:=range []*os.File{l.parentDirectory,l.directory,l.lock} { if _,_,err:=expiryRootStat(f);err!=nil { t.Fatal("retained original descriptor lost",err) } }
 if !errors.Is(l.rollbackControl(c,nil),primary)||l.control!=c { t.Fatal("residual retry lost FIRST") }
 if err:=l.closeDescriptors(c);err==nil { t.Fatal("unsafe residual gained automatic disposal authority") }
 if l.cleanup.first.Load()!=first||c.firstErr!=primary {t.Fatal("canonical retry overwrote original FIRST")}
}
func h06LateOriginalOwnerDrained(t *testing.T,l *expiryHostLease,c *expiryLeaseControl){
 t.Helper();r:=c.candidate
 if r==nil||r.lease!=l||c.predecessor!=nil||c.metadata!=nil||r.owner==nil||!r.ownerCloseRecorded||r.firstOwnerCloseErr!=nil||r.host!=nil||r.gate!=nil||r.hostCloseRecorded||r.gateCloseRecorded.Load(){t.Fatal("actual original component owner FIRST association/outcome absent")}
 if !r.invalid.Load()||!r.controlledTeardown.Load()||r.window==nil||!r.window.retired||!r.window.completed||r.window.operation!=nil||r.window.metadataCount!=0||r.window.metadata!=nil {t.Fatal("actual original retirement/drain did not complete")}
 select{case <-r.window.done:default:t.Fatal("original retired-and-zero completion not published")}
 // Reading the original owner's already-closed File is safe: never close a
 // raw reused descriptor or publish synthetic completion/error bookkeeping.
 f,ok:=r.owner.closer.(*os.File);if !ok{t.Fatal("real component owner File absent")}
 if _,err:=f.Stat();!errors.Is(err,os.ErrClosed){t.Fatal("actual original owner Close not reached",err)}
}
func TestExpiryH06LateRollbackMetadataActualMethods(t *testing.T) {
 for _,site:=range []string{"identity","path","positive"} { t.Run(site,func(t *testing.T) {
  l:=h06LateReserve(t);c:=l.control;primary:=errors.New("H06 ORIGINAL constructor primary")
  // The reserve check succeeded; mutations are real, owned, and occur before
  // the FINAL rollback check, AFTER the earlier cleanup.outcome(false) can be nil.
  switch site {
  case "identity": if err:=os.Chmod(l.controlRoot,0750);err!=nil { t.Fatal(err) }
  case "path": if err:=os.Rename(l.controlRoot,l.controlRoot+".original-preserved");err!=nil { t.Fatal(err) }
  }
  got:=l.rollbackControl(c,primary) // actual retire/drain/check/residual bodies
  if site=="positive" {
   if !errors.Is(got,primary)||c.firstErr!=primary||l.control!=c||c.stage!=expiryControlTerminalClosed||l.residualPinned||l.cleanup.outcome(true)!=nil { t.Fatal("safe original 111 positive changed",got) }
   h06LateOriginalOwnerDrained(t,l,c);h06LateCounts(t,l,false,2)
  } else {
   field:="expiry_root_identity";m:=uint64(1);if site=="path" { field="expiry_root_path";m=2 }
   h06LateResidual(t,l,c,primary,got,field);h06LateCounts(t,l,true,m)
  }
 }) }
}

func TestExpiryH06LateRollbackSourceReleaseEmbargo(t *testing.T) {
 if h06LateSelectedProfile != "normal" || h06LateSelectedSourcePath != "/home/user/Documents/code/spec-kitty-redesign/internal/productionruntime/expiry_qualification_rundown.go" || h06LateSelectedSourceSHA256 != "ae2f036949803725a4c5c3b21c4e20746a9d06765f71930cf56aa7ac421101c2" { t.Fatal("wrong fixed profile/path/hash selection") }
 raw,err:=os.ReadFile(h06LateSelectedSourcePath);if err!=nil { t.Fatal(err) }
 if fmt.Sprintf("%x",sha256.Sum256(raw))!=h06LateSelectedSourceSHA256 { t.Fatal("selected full source SHA256 mismatch") }
 // This is the exact declared compiler-input member, NOT a working-directory
 // lookup or a cached pure-product proxy for an instrumented selected body.
 h06LateAssertRollbackSource(t,raw)
}

func h06LateAssertRollbackSource(t *testing.T,raw []byte) {
 t.Helper()
 set:=token.NewFileSet();file,err:=parser.ParseFile(set,h06LateSelectedSourcePath,raw,0);if err!=nil { t.Fatal(err) }
 var fn *ast.FuncDecl;methodCount:=0
 text:=func(n ast.Node)string{return string(raw[set.Position(n.Pos()).Offset:set.Position(n.End()).Offset])}
 for _,decl:=range file.Decls { if f,ok:=decl.(*ast.FuncDecl);ok&&f.Name.Name=="rollbackControl" { fn=f;methodCount++ } }
 if methodCount!=1||fn==nil||fn.Body==nil||fn.Recv==nil||len(fn.Recv.List)!=1||text(fn.Recv.List[0])!="l *expiryHostLease" {t.Fatal("original rollback method absent/ambiguous")}
 body:=text(fn.Body)
 // Inspect call ASTs as well as the original textual counts. Deferred local
 // cancel is required and distinct from a direct test-only cancel statement.
 checks,releases,retires,drains,localCancels,deferredCancels:=0,0,0,0,0,0
 ast.Inspect(fn.Body,func(n ast.Node)bool {
  switch x:=n.(type) {
  case *ast.CallExpr:
   switch text(x.Fun) {
   case "l.checkPathsForControl": checks++;if text(x)!="l.checkPathsForControl(c)"{t.Fatal("changed final check owner")}
   case "l.closeDescriptors": releases++;if text(x)!="l.closeDescriptors(c)"{t.Fatal("changed release owner")}
   case "l.retireLocked": retires++;if text(x)!="l.retireLocked(r)"{t.Fatal("changed original retirement")}
   case "l.drainControl": drains++;if text(x)!="l.drainControl(cleanup, c)"{t.Fatal("changed original drain")}
   case "cancel": localCancels++;if len(x.Args)!=0{t.Fatal("non-original cancel")}
   }
  case *ast.DeferStmt:
   if text(x.Call)=="cancel()" {deferredCancels++}
  }
  return true
 })
 if checks!=1||releases!=1||retires!=1||drains!=1||deferredCancels!=1 {t.Fatal("original check/release/retirement/drain/deferred-cancel census")}
 found:=0;frontier:=-1
 for i,stmt:=range fn.Body.List {
  full,ok:=stmt.(*ast.IfStmt);if !ok||text(full.Cond)!="l.parentDirectory != nil && l.directory != nil && l.lock != nil"{continue}
  found++;frontier=i
  if full.Init!=nil||full.Else!=nil||len(full.Body.List)!=1||text(full.Body.List[0])!="outcome = errors.Join(outcome, l.checkPathsForControl(c))" {t.Fatal("original full-root conditional/partial branch changed")}
  next:=i+1
  if localCancels!=1 {t.Fatal("normal product forbids any non-deferred local cancel, including frontier")}
  if next+1>=len(fn.Body.List){t.Fatal("safety/release adjacency absent")}
  guard,ok:=fn.Body.List[next].(*ast.IfStmt)
  if !ok||guard.Init!=nil||guard.Else!=nil||text(guard.Cond)!="cleanup.Err() != nil || outcome != nil"||len(guard.Body.List)!=1||text(guard.Body.List[0])!="return l.residual(c, errors.Join(cause, outcome, cleanup.Err()))" {t.Fatal("strict post-metadata safety/residual ordered union changed")}
  if text(fn.Body.List[next+1])!="outcome = errors.Join(outcome, l.closeDescriptors(c))"{t.Fatal("sole root release not immediately after safety guard")}
 }
 if found!=1||frontier<0{t.Fatal("full-root safety frontier ambiguous/missing")}
 // Preserve original top-level retire/invalidate/drain ordering, not dead
 // comment tokens or an uncalled helper with the same name.
 retirementAt,invalidationAt,drainAt,contextAt:=-1,-1,-1,-1
 for i,stmt:=range fn.Body.List {
  if loop,ok:=stmt.(*ast.RangeStmt);ok&&text(loop.X)=="[]*expiryHostRun{c.predecessor, c.candidate}" {
   if len(loop.Body.List)==2&&text(loop.Body.List[0])=="l.retireLocked(r)"&&text(loop.Body.List[1])=="if r != nil {\n\t\t\tr.controlledTeardown.Store(true)\n\t\t}" {retirementAt=i}
   if len(loop.Body.List)==1&&text(loop.Body.List[0])=="if r != nil {\n\t\t\tr.invalidate()\n\t\t}" {invalidationAt=i}
  }
  if text(stmt)=="cleanup, cancel := expiryCleanupContext()" {contextAt=i}
  if branch,ok:=stmt.(*ast.IfStmt);ok&&branch.Init!=nil&&text(branch.Init)=="err := l.drainControl(cleanup, c)"&&text(branch.Cond)=="err != nil"&&branch.Else==nil&&len(branch.Body.List)==1&&text(branch.Body.List[0])=="return errors.Join(cause, err)" {drainAt=i}
 }
 if retirementAt<0||contextAt<=retirementAt||invalidationAt<=contextAt||drainAt<=invalidationAt||drainAt>=frontier {t.Fatal("original retire/invalidate/independent-context/drain ordering lost")}
 check:=strings.Index(body,"outcome = errors.Join(outcome, l.checkPathsForControl(c))")
 closeAt:=strings.Index(body,"outcome = errors.Join(outcome, l.closeDescriptors(c))")
 if check<0||closeAt<check {t.Fatal("actual final-check/root-close anchors absent")}
 interval:=body[check:closeAt]
 for _,required:=range []string{"if cleanup.Err() != nil || outcome != nil {","return l.residual(c, errors.Join(cause, outcome, cleanup.Err()))"} {if !strings.Contains(interval,required){t.Fatal("final safety cutoff/ordered union missing",required)}}
 if strings.Count(body,"l.closeDescriptors(c)")!=1||strings.Count(body,"l.checkPathsForControl(c)")!=1 {t.Fatal("uncensused release/check route")}
 // Preserve the original operation embargo. No manufactured identity or
 // alternate root-close authority is introduced by either profile.
 for _,bad:=range []string{".Close()","closeFile(","ownFD(","lockIdentity =","parentIdentity =","directoryIdentity ="} {if strings.Contains(body,bad){t.Fatal("unrelated root operation in rollback",bad)}}
}
