# 通用插件契约与执行核心 Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Tasks have sequential dependencies; keep this first phase local rather than delegating blocking work.

**Goal:** 新增可测试的通用结果契约、业务分组选择器、串行处理链与独立任务结果，不改变已有生产请求行为。

**Architecture:** 使用独立的版本化领域包定义输入输出，纯执行器仅依赖领域包。执行器匹配宿主提供的分组并执行插件决定，不判断风险分数；运行适配器、数据库、安装包扩展、严格响应缓冲和管理 UI 留在总计划的独立后续阶段。

**Tech Stack:** Go 标准库、当前项目模块 `github.com/Wei-Shaw/sub2api`；不增加第三方依赖，不修改旧 `pluginapi/v1` RPC。

---

## 执行前须知

- 设计依据：`docs/superpowers/specs/2026-09-30-general-plugin-platform-design.md`；总路线图：`docs/superpowers/plans/2026-09-30-general-plugin-platform.md`。
- 当前已有大量未提交开发结果。先保存工作区清单与基线；本阶段只新增下面列出的八个文件。
- 若任何目标文件已存在，先阅读并合并计划，不覆盖；若生成计划后仓库状态变化，重新核对模块路径和现有插件回归测试。
- 所有测试命令在项目 `backend` 目录运行。命令预期不是当前项目已通过的声明。
- 实现采用红→绿：先写测试运行确认缺失符号失败，再加入实现；不得用永久 `Skip` 或删除断言规避失败。
- 每项完成保留独立差异检查点；提交、推送需要另行确认，不把技能中的频繁提交建议作为自动提交授权。
- 这是一份领域代码的可执行计划，不是新 RPC 线格式或全部平台功能的完成声明。

## 文件结构与依赖

```text
pkg/pluginapi/contracts/v1/
  hook.go            # 输入输出和决策合法性
  hook_test.go
  task.go            # 与请求链分离的任务结果
  task_test.go
internal/pluginflow/
  binding.go         # 精确分组匹配、固定实例版本、稳定顺序
  binding_test.go
  chain.go           # 串行执行与短路，无网络/数据库
  chain_test.go
```

依赖顺序：任务 1 → 任务 2 → 任务 3；任务 4 只依赖任务 1，但本阶段可顺序完成。

## Task 1：定义阶段、内容与插件决策

**Files:**
- Create: `backend/pkg/pluginapi/contracts/v1/hook.go`
- Test: `backend/pkg/pluginapi/contracts/v1/hook_test.go`

- [x] **Step 1：新增完整测试文件。**

#### File: backend/pkg/pluginapi/contracts/v1/hook_test.go

```go
package contractv1

import (
    "math"
    "testing"
)

func TestValidateHookResult(t *testing.T) {
    high, low, invalid, nan, infinite := 0.99, 0.01, 1.1, math.NaN(), math.Inf(1)
    rejection := &Rejection{Code: "policy_blocked", Message: "Content blocked"}
    content := &Content{MediaType: "text/plain", Data: []byte("clean")}
    tests := []struct {
        name string
        result HookResult
        canReplace bool
        valid bool
    }{
        {"pass", HookResult{Decision: Pass}, false, true},
        {"plugin pass wins over high confidence", HookResult{Decision: Pass, Findings: []Finding{{Confidence: &high}}}, false, true},
        {"plugin block wins over low confidence", HookResult{Decision: Block, Rejection: rejection, Findings: []Finding{{Confidence: &low}}}, false, true},
        {"replace", HookResult{Decision: Replace, Content: content}, true, true},
        {"empty replacement is explicit", HookResult{Decision: Replace, Content: &Content{MediaType: "text/plain", Data: []byte{}}}, true, true},
        {"unknown decision", HookResult{Decision: "allow"}, false, false},
        {"pass cannot carry replacement", HookResult{Decision: Pass, Content: content}, true, false},
        {"pass cannot carry rejection", HookResult{Decision: Pass, Rejection: rejection}, false, false},
        {"replace must be authorized", HookResult{Decision: Replace, Content: content}, false, false},
        {"replace needs content", HookResult{Decision: Replace}, true, false},
        {"replace needs media type", HookResult{Decision: Replace, Content: &Content{}}, true, false},
        {"replace cannot reject", HookResult{Decision: Replace, Content: content, Rejection: rejection}, true, false},
        {"block needs rejection", HookResult{Decision: Block}, false, false},
        {"block cannot return original body", HookResult{Decision: Block, Content: content, Rejection: rejection}, false, false},
        {"block needs machine code", HookResult{Decision: Block, Rejection: &Rejection{Code: "bad code", Message: "Blocked"}}, false, false},
        {"confidence out of range", HookResult{Decision: Pass, Findings: []Finding{{Confidence: &invalid}}}, false, false},
        {"confidence nan", HookResult{Decision: Pass, Findings: []Finding{{Confidence: &nan}}}, false, false},
        {"confidence infinite", HookResult{Decision: Pass, Findings: []Finding{{Confidence: &infinite}}}, false, false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateHookResult(tt.result, tt.canReplace)
            if (err == nil) != tt.valid {
                t.Fatalf("valid=%v, error=%v", tt.valid, err)
            }
        })
    }
}

func TestCloneContentOwnsBytes(t *testing.T) {
    original := Content{MediaType: "text/plain", Data: []byte("original")}
    cloned := original.Clone()
    cloned.Data[0] = 'X'
    if string(original.Data) != "original" {
        t.Fatal("content copy shares backing memory")
    }
}
```

- [x] **Step 2：运行红测试。**

```powershell
go test ./pkg/pluginapi/contracts/v1 -run 'TestValidateHookResult|TestCloneContentOwnsBytes' -count=1
```

预期：编译失败，提示 `Rejection`、`HookResult` 等尚未定义。确认这是新代码缺失而不是工具链或依赖错误。

- [x] **Step 3：新增完整实现文件。**

#### File: backend/pkg/pluginapi/contracts/v1/hook.go

```go
package contractv1

import (
    "bytes"
    "errors"
    "fmt"
    "math"
    "mime"
    "regexp"
    "strings"
    "unicode/utf8"
)

type Stage string

const (
    Input Stage = "input"
    Output Stage = "output"
)

func (s Stage) Valid() bool { return s == Input || s == Output }

type Decision string

const (
    Pass Decision = "pass"
    Replace Decision = "replace"
    Block Decision = "block"
)

type Content struct {
    MediaType string `json:"media_type"`
    Data []byte `json:"data"`
}

func (c Content) Clone() Content {
    c.Data = bytes.Clone(c.Data)
    return c
}

type HookRequest struct {
    RequestID string `json:"request_id"`
    AttemptID string `json:"attempt_id"`
    GroupID int64 `json:"group_id"`
    Stage Stage `json:"stage"`
    Content Content `json:"content"`
}

type Finding struct {
    RuleID string `json:"rule_id,omitempty"`
    Category string `json:"category,omitempty"`
    Confidence *float64 `json:"confidence,omitempty"`
}

type Rejection struct {
    Code string `json:"code"`
    Message string `json:"message"`
}

type HookResult struct {
    Decision Decision `json:"decision"`
    Content *Content `json:"content,omitempty"`
    Rejection *Rejection `json:"rejection,omitempty"`
    Findings []Finding `json:"findings,omitempty"`
}

var ErrInvalidResult = errors.New("invalid plugin hook result")
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func ValidateHookResult(r HookResult, canReplace bool) error {
    invalid := func(reason string) error {
        return fmt.Errorf("%w: %s", ErrInvalidResult, reason)
    }
    if len(r.Findings) > 128 {
        return invalid("too many findings")
    }
    for _, finding := range r.Findings {
        if finding.Confidence == nil {
            continue
        }
        score := *finding.Confidence
        if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
            return invalid("confidence outside [0,1]")
        }
    }
    switch r.Decision {
    case Pass:
        if r.Content != nil || r.Rejection != nil {
            return invalid("pass contains an action payload")
        }
    case Replace:
        if !canReplace || r.Content == nil || r.Rejection != nil {
            return invalid("replacement missing or not authorized")
        }
        mediaType, _, err := mime.ParseMediaType(r.Content.MediaType)
        if err != nil || !strings.Contains(mediaType, "/") {
            return invalid("replacement media type missing or invalid")
        }
    case Block:
        if r.Content != nil || r.Rejection == nil || !codePattern.MatchString(r.Rejection.Code) {
            return invalid("block payload invalid")
        }
        message := r.Rejection.Message
        if !utf8.ValidString(message) || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > 512 {
            return invalid("client message invalid")
        }
    default:
        return invalid("unknown decision")
    }
    return nil
}
```

`Confidence` 仅用于记录与格式校验；代码不依赖分数决定 `pass/block`。客户端提示的协议编码和敏感内容脱敏由后续宿主适配承担，长度校验不等于语义脱敏。

- [x] **Step 4：运行相同测试，预期退出 0；运行 `gofmt` 后再次运行。**

```powershell
gofmt -w pkg/pluginapi/contracts/v1/hook.go pkg/pluginapi/contracts/v1/hook_test.go
go test ./pkg/pluginapi/contracts/v1 -count=1
```

## Task 2：业务分组选择与固定实例引用

**Files:**
- Create: `backend/internal/pluginflow/binding.go`
- Test: `backend/internal/pluginflow/binding_test.go`

- [x] **Step 1：新增完整测试文件。**

#### File: backend/internal/pluginflow/binding_test.go

```go
package pluginflow

import (
    "errors"
    "reflect"
    "testing"

    contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

func fixtureBinding(id, group int64, stage contract.Stage, order int) Binding {
    return Binding{ID: id, InstanceID: id + 100, GroupID: group, Stage: stage,
        Order: order, Enabled: true, Ready: true, Version: "1.0.0", ConfigRevision: 1}
}

func TestSelectBindingsScopeAndOrder(t *testing.T) {
    disabled := fixtureBinding(4, 7, contract.Input, 0)
    disabled.Enabled = false
    otherGroup := fixtureBinding(5, 8, contract.Input, 0)
    otherGroup.Ready = false
    bindings := []Binding{
        fixtureBinding(9, 7, contract.Input, 20),
        fixtureBinding(3, 7, contract.Input, 10),
        fixtureBinding(2, 7, contract.Input, 10),
        fixtureBinding(6, 7, contract.Output, 0), disabled, otherGroup,
    }
    original := append([]Binding(nil), bindings...)
    got, err := SelectBindings(bindings, 7, contract.Input)
    if err != nil { t.Fatal(err) }
    ids := []int64{}
    for _, binding := range got { ids = append(ids, binding.ID) }
    if !reflect.DeepEqual(ids, []int64{2, 3, 9}) { t.Fatalf("ids=%v", ids) }
    if !reflect.DeepEqual(bindings, original) { t.Fatal("caller bindings were reordered") }
    got[0].Version = "changed"
    if !reflect.DeepEqual(bindings, original) { t.Fatal("snapshot aliases caller bindings") }
}

func TestSelectBindingsRejectsInvalidSnapshots(t *testing.T) {
    tests := []struct{name string; edit func(*Binding)}{
        {"id", func(b *Binding) { b.ID = 0 }},
        {"instance", func(b *Binding) { b.InstanceID = 0 }},
        {"version", func(b *Binding) { b.Version = "" }},
        {"revision", func(b *Binding) { b.ConfigRevision = 0 }},
        {"unavailable", func(b *Binding) { b.Ready = false }},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            b := fixtureBinding(1, 7, contract.Input, 0)
            tt.edit(&b)
            got, err := SelectBindings([]Binding{b}, 7, contract.Input)
            if !errors.Is(err, ErrBindingUnavailable) || got != nil {
                t.Fatalf("got=%v err=%v", got, err)
            }
        })
    }
    b := fixtureBinding(1, 7, contract.Input, 0)
    if _, err := SelectBindings([]Binding{b, b}, 7, contract.Input); err == nil {
        t.Fatal("duplicate binding accepted")
    }
    if _, err := SelectBindings(nil, 0, contract.Input); !errors.Is(err, ErrInvalidContext) {
        t.Fatal("missing actual group accepted")
    }
    if _, err := SelectBindings(nil, 7, "unknown"); !errors.Is(err, ErrInvalidContext) {
        t.Fatal("unknown stage accepted")
    }
}
```

- [x] **Step 2：运行红测试。**

```powershell
go test ./internal/pluginflow -run TestSelectBindings -count=1
```

预期：缺少 `Binding`、`SelectBindings` 等定义导致失败。

- [x] **Step 3：新增完整实现文件。**

#### File: backend/internal/pluginflow/binding.go

```go
package pluginflow

import (
    "errors"
    "fmt"
    "sort"
    "strings"

    contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

var ErrInvalidContext = errors.New("invalid plugin execution context")
var ErrBindingUnavailable = errors.New("plugin binding unavailable")

type Binding struct {
    ID int64
    InstanceID int64
    GroupID int64
    Stage contract.Stage
    Order int
    Enabled bool
    Ready bool
    Version string
    ConfigRevision int64
    CanReplace bool
}

func SelectBindings(all []Binding, groupID int64, stage contract.Stage) ([]Binding, error) {
    if groupID <= 0 || !stage.Valid() {
        return nil, ErrInvalidContext
    }
    selected := make([]Binding, 0)
    seen := make(map[int64]struct{})
    for _, b := range all {
        if !b.Enabled || b.GroupID != groupID || b.Stage != stage {
            continue
        }
        if b.ID <= 0 || b.InstanceID <= 0 || !b.Ready || strings.TrimSpace(b.Version) == "" || b.ConfigRevision <= 0 {
            return nil, fmt.Errorf("%w: binding=%d", ErrBindingUnavailable, b.ID)
        }
        if _, exists := seen[b.ID]; exists {
            return nil, fmt.Errorf("%w: duplicate binding=%d", ErrBindingUnavailable, b.ID)
        }
        seen[b.ID] = struct{}{}
        selected = append(selected, b)
    }
    sort.Slice(selected, func(i, j int) bool {
        if selected[i].Order == selected[j].Order {
            return selected[i].ID < selected[j].ID
        }
        return selected[i].Order < selected[j].Order
    })
    return selected, nil
}
```

这里 `Ready`、版本和配置修订来自可信宿主快照，不来自客户端输入。P2 必须解析到实际固定运行句柄并保持生命周期；这个选择器本身不宣称已经解决热更新。

- [x] **Step 4：格式化后运行绿测试，预期退出 0。**

```powershell
gofmt -w internal/pluginflow/binding.go internal/pluginflow/binding_test.go
go test ./internal/pluginflow -run TestSelectBindings -count=1
```

## Task 3：串行调用、结果短路及错误隔离

**Files:**
- Create: `backend/internal/pluginflow/chain.go`
- Test: `backend/internal/pluginflow/chain_test.go`

- [x] **Step 1：新增完整测试文件。**

#### File: backend/internal/pluginflow/chain_test.go

```go
package pluginflow

import (
    "context"
    "errors"
    "strings"
    "testing"

    contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

func fixtureRequest() contract.HookRequest {
    return contract.HookRequest{RequestID: "request-1", AttemptID: "attempt-1",
        GroupID: 7, Stage: contract.Input,
        Content: contract.Content{MediaType: "text/plain", Data: []byte("original")}}
}

func TestRunUnboundDoesNotInvoke(t *testing.T) {
    bindings := []Binding{fixtureBinding(1, 8, contract.Input, 0)}
    out, err := Run(context.Background(), fixtureRequest(), bindings, nil)
    if err != nil || out.Decision != contract.Pass || out.Content == nil || string(out.Content.Data) != "original" {
        t.Fatalf("out=%+v err=%v", out, err)
    }
}

func TestRunReplacementFeedsNextPlugin(t *testing.T) {
    first := fixtureBinding(1, 7, contract.Input, 0)
    first.CanReplace = true
    second := fixtureBinding(2, 7, contract.Input, 1)
    calls := 0
    invoke := func(_ context.Context, b Binding, req contract.HookRequest) (contract.HookResult, error) {
        calls++
        if b.ID == 1 {
            content := contract.Content{MediaType: "text/plain", Data: []byte("clean")}
            return contract.HookResult{Decision: contract.Replace, Content: &content}, nil
        }
        if string(req.Content.Data) != "clean" { t.Fatal("next plugin did not receive replacement") }
        return contract.HookResult{Decision: contract.Pass}, nil
    }
    out, err := Run(context.Background(), fixtureRequest(), []Binding{second, first}, invoke)
    if err != nil || calls != 2 || out.Content == nil || string(out.Content.Data) != "clean" {
        t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
    }
}

func TestRunBlockStopsAndReturnsNoOriginalContent(t *testing.T) {
    req := fixtureRequest()
    req.Stage = contract.Output
    bindings := []Binding{fixtureBinding(1, 7, contract.Output, 0), fixtureBinding(2, 7, contract.Output, 1)}
    calls := 0
    invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
        calls++
        score := 0.01
        return contract.HookResult{Decision: contract.Block,
            Rejection: &contract.Rejection{Code: "business_policy", Message: "Blocked"},
            Findings: []contract.Finding{{Confidence: &score}}}, nil
    }
    out, err := Run(context.Background(), req, bindings, invoke)
    if err != nil || calls != 1 || out.Decision != contract.Block || out.Content != nil || out.Rejection == nil {
        t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
    }
}

func TestRunPassCannotMutateInput(t *testing.T) {
    req := fixtureRequest()
    invoke := func(_ context.Context, _ Binding, input contract.HookRequest) (contract.HookResult, error) {
        input.Content.Data[0] = 'X'
        score := 0.99
        return contract.HookResult{Decision: contract.Pass, Findings: []contract.Finding{{Confidence: &score}}}, nil
    }
    out, err := Run(context.Background(), req, []Binding{fixtureBinding(1, 7, contract.Input, 0)}, invoke)
    if err != nil || out.Content == nil || string(out.Content.Data) != "original" || string(req.Content.Data) != "original" {
        t.Fatalf("out=%+v err=%v", out, err)
    }
}

func TestRunFailuresReturnNoPartialContent(t *testing.T) {
    tests := []struct{name string; invoke Invoker; target error}{
        {"missing invoker", nil, ErrInvocation},
        {"plugin error", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
            return contract.HookResult{}, errors.New("secret-upstream-token")
        }, ErrInvocation},
        {"panic", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
            panic("secret-upstream-token")
        }, ErrInvocation},
        {"unknown result", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
            return contract.HookResult{Decision: "unknown"}, nil
        }, contract.ErrInvalidResult},
        {"unauthorized replace", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
            content := contract.Content{MediaType: "text/plain", Data: []byte("changed")}
            return contract.HookResult{Decision: contract.Replace, Content: &content}, nil
        }, contract.ErrInvalidResult},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            out, err := Run(context.Background(), fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0)}, tt.invoke)
            if !errors.Is(err, tt.target) || out.Content != nil || out.Rejection != nil {
                t.Fatalf("out=%+v err=%v", out, err)
            }
            if strings.Contains(err.Error(), "secret-upstream-token") { t.Fatal("raw error leaked") }
        })
    }
}

func TestRunCancellationDiscardsResult(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
        cancel()
        return contract.HookResult{Decision: contract.Pass}, nil
    }
    out, err := Run(ctx, fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0)}, invoke)
    if !errors.Is(err, context.Canceled) || out.Content != nil { t.Fatalf("out=%+v err=%v", out, err) }
}
```

- [x] **Step 2：运行红测试。**

```powershell
go test ./internal/pluginflow -run TestRun -count=1
```

预期：缺少 `Run` 和 `Invoker` 导致失败。既有选择器测试继续保留。

- [x] **Step 3：新增完整实现文件。**

#### File: backend/internal/pluginflow/chain.go

```go
package pluginflow

import (
    "context"
    "errors"
    "fmt"

    contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

var ErrInvocation = errors.New("plugin invocation failed")

type Invoker func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error)

type Outcome struct {
    Decision contract.Decision
    Content *contract.Content
    Rejection *contract.Rejection
}

func invokeProtected(ctx context.Context, b Binding, req contract.HookRequest, invoke Invoker) (result contract.HookResult, err error) {
    defer func() {
        if recover() != nil {
            result = contract.HookResult{}
            err = fmt.Errorf("%w: binding=%d", ErrInvocation, b.ID)
        }
    }()
    if invoke == nil { return contract.HookResult{}, ErrInvocation }
    result, err = invoke(ctx, b, req)
    if err != nil {
        return contract.HookResult{}, fmt.Errorf("%w: binding=%d", ErrInvocation, b.ID)
    }
    return result, nil
}

func Run(ctx context.Context, req contract.HookRequest, bindings []Binding, invoke Invoker) (Outcome, error) {
    if err := ctx.Err(); err != nil { return Outcome{}, err }
    selected, err := SelectBindings(bindings, req.GroupID, req.Stage)
    if err != nil { return Outcome{}, err }
    current := req.Content.Clone()
    for _, binding := range selected {
        if err := ctx.Err(); err != nil { return Outcome{}, err }
        call := req
        call.Content = current.Clone()
        result, err := invokeProtected(ctx, binding, call, invoke)
        if contextErr := ctx.Err(); contextErr != nil { return Outcome{}, contextErr }
        if err != nil { return Outcome{}, err }
        if err := contract.ValidateHookResult(result, binding.CanReplace); err != nil {
            return Outcome{}, fmt.Errorf("binding=%d: %w", binding.ID, err)
        }
        switch result.Decision {
        case contract.Pass:
        case contract.Replace:
            current = result.Content.Clone()
        case contract.Block:
            rejection := *result.Rejection
            return Outcome{Decision: contract.Block, Rejection: &rejection}, nil
        }
    }
    return Outcome{Decision: contract.Pass, Content: &current}, nil
}
```

这里的 `Outcome.Pass` 表示整条链允许当前内容继续，不表示内容从未被替换。此代码没有对客户端写出的权限，也没有触发上游请求。

`Invoker` 是宿主运行适配器的测试接缝。P2 必须把 context 截止时间传入真实 RPC 并实现运行资源回收；在调用前后检查 context 不等于能强制中断任意不合作的进程内代码。正文大小、协议结构校验、日志脱敏及严格缓冲由后续阶段负责。

- [x] **Step 4：格式化，运行绿测试及前两项回归。**

```powershell
gofmt -w internal/pluginflow/chain.go internal/pluginflow/chain_test.go
go test ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=1
```

预期：两个包退出 0；低置信度 `block` 和高置信度 `pass` 都原样执行插件决定。

## Task 4：独立任务结果与最终阶段验收

**Files:**
- Create: `backend/pkg/pluginapi/contracts/v1/task.go`
- Test: `backend/pkg/pluginapi/contracts/v1/task_test.go`

- [x] **Step 1：新增完整测试文件。**

#### File: backend/pkg/pluginapi/contracts/v1/task_test.go

```go
package contractv1

import (
    "encoding/json"
    "testing"
)

func TestValidateTaskResult(t *testing.T) {
    tests := []struct{name string; result TaskResult; valid bool}{
        {"success no change", TaskResult{Status: TaskSucceeded}, true},
        {"success changed", TaskResult{Status: TaskSucceeded, Changed: true, Output: json.RawMessage(`{"version":2}`)}, true},
        {"failure", TaskResult{Status: TaskFailed, ErrorCode: "upstream_unavailable"}, true},
        {"partial commits are explicit", TaskResult{Status: TaskFailed, Changed: true, ErrorCode: "partial_failure"}, true},
        {"hook decision is not task status", TaskResult{Status: "pass"}, false},
        {"success with failure code", TaskResult{Status: TaskSucceeded, ErrorCode: "failed"}, false},
        {"failure requires code", TaskResult{Status: TaskFailed}, false},
        {"failure code is structured", TaskResult{Status: TaskFailed, ErrorCode: "raw error text"}, false},
        {"invalid json", TaskResult{Status: TaskSucceeded, Output: json.RawMessage(`{`)}, false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if err := ValidateTaskResult(tt.result); (err == nil) != tt.valid {
                t.Fatalf("valid=%v error=%v", tt.valid, err)
            }
        })
    }
}
```

- [x] **Step 2：运行红测试。**

```powershell
go test ./pkg/pluginapi/contracts/v1 -run TestValidateTaskResult -count=1
```

预期：缺少 `TaskResult` 定义而失败。

- [x] **Step 3：新增完整实现文件。**

#### File: backend/pkg/pluginapi/contracts/v1/task.go

```go
package contractv1

import (
    "encoding/json"
    "errors"
)

type TaskStatus string

const (
    TaskSucceeded TaskStatus = "succeeded"
    TaskFailed TaskStatus = "failed"
)

type TaskResult struct {
    Status TaskStatus `json:"status"`
    Changed bool `json:"changed"`
    ErrorCode string `json:"error_code,omitempty"`
    Output json.RawMessage `json:"output,omitempty"`
}

var ErrInvalidTaskResult = errors.New("invalid plugin task result")

func ValidateTaskResult(r TaskResult) error {
    switch r.Status {
    case TaskSucceeded:
        if r.ErrorCode != "" { return ErrInvalidTaskResult }
    case TaskFailed:
        if !codePattern.MatchString(r.ErrorCode) { return ErrInvalidTaskResult }
    default:
        return ErrInvalidTaskResult
    }
    if len(r.Output) > 0 && !json.Valid(r.Output) { return ErrInvalidTaskResult }
    return nil
}
```

`Changed` 允许失败时为 true，表示失败前已有明确业务变更；宿主后续用幂等记录和更新事务核对，不宣称失败一定意味着全部回滚。调度状态与租约不属于这个插件结果类型。

- [x] **Step 4：运行绿测试、格式与静态检查。**

```powershell
gofmt -w pkg/pluginapi/contracts/v1/task.go pkg/pluginapi/contracts/v1/task_test.go
go test ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=1
go vet ./pkg/pluginapi/contracts/v1 ./internal/pluginflow
git diff --check
```

预期全部退出 0。注意 `git diff --check` 不自动检查未跟踪文件；另用 `gofmt -l pkg/pluginapi/contracts/v1 internal/pluginflow` 确认无输出，并逐个核对新增文件。

- [x] **Step 5：执行旧插件兼容回归，记录实际输出。**

```powershell
go test ./pkg/pluginapi/v1 ./internal/service -run 'Plugin' -count=1 -timeout=180s
```

预期退出 0。若现有测试有基线问题，应记录并区分，不删除旧测试、不将新纯包测试通过作为旧插件兼容证明。

- [x] **Step 6：审查本轮写集与回退。**

确认只有本计划的八个新增 Go 文件和必要的进度记录。原有业务源文件与协议哈希应保持不变。以阶段开始时的快照为基准，在独立副本验证仅撤销本轮新增文件能恢复基线；实际开发工作区保留实现。未经另行确认不提交、不推送。

## 完成定义

- 八个文件全部实现并格式化，红／绿测试过程留有记录。
- 选择器证明未绑定不调用、不可用绑定不旁路、顺序稳定。
- 执行器证明插件决定不被风险分数覆盖、原文不出现在阻断结果、替换需授权、异常不返回部分内容。
- 独立任务结果与请求链决策分离。
- 旧插件定向回归通过，或准确记录尚未解决的基线失败，不误报阶段完成。
- 完成后进入 P2 的详细计划和接口审查；此阶段没有安装新能力、没有真实网关严格审核、没有定时任务上线。

## 计划编写后的独立自查（2026-09-30）

已从本文提取八个完整 Go 代码块，在独立临时模块 `D:\tmp\sub2api-plugin-plan-20260930` 中格式化、编译和测试：

```text
go version
→ go version go1.27.0 windows/amd64

go test ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=1
→ 两个包均 ok；exit 0

go vet ./pkg/pluginapi/contracts/v1 ./internal/pluginflow
→ 无输出；exit 0
```

原始命令、执行目录、输出和退出码保存在临时模块的 `checks.json`。

自查同时确认：八个计划目标文件在实际项目中尚不存在；两份计划的 Markdown 代码围栏配对；总路线图覆盖设计稿的全部 16 项验收场景；第一阶段所有类型、导入与函数签名在临时模块中可编译。

这是对计划示例的可执行性校验，不是项目实现交付、生产网关测试、数据库验证或 Docker 验收。实际项目只新增计划文档，尚未创建上述八个 Go 文件。

## 项目实施记录（2026-09-30）

上述“计划编写后的独立自查”是实施前历史记录；本阶段现已在实际项目中新增八个 Go 文件。实现未改动既有 transport-v1、业务网关或 RBAC 源码。

- 四项任务均保留 RED / GREEN 记录。
- 新包测试、20 次重复测试、race、vet、旧插件定向回归及服务端构建通过。
- contracts/v1 语句覆盖率 100.0%，pluginflow 98.0%。
- 审查追加组合测试：替换后取消／非法结果必须返回空 Outcome；健康与不可用绑定混排时必须在任何调用前失败。
- 本轮增量包、基线哈希和独立副本回滚记录位于 D:\tmp\sub2api-plugin-phase1-20260930；详细原始输出见 VERIFICATION.txt。
- 阶段边界与下一阶段交接见同目录的 2026-09-30-general-plugin-phase1-handoff.md。