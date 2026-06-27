# mobile-detect (Go)

> [English](README.md) | 简体中文

[![Go Reference](https://pkg.go.dev/badge/github.com/anhao/go-mobile-detect.svg)](https://pkg.go.dev/github.com/anhao/go-mobile-detect)

一个轻量、地道的 Go 语言移植版本，源自 [PHP **Mobile-Detect**](https://github.com/serbanghita/Mobile-Detect) 库，用于通过 User-Agent 字符串识别移动设备（包括平板），并可结合一组强信号的 HTTP 头信息来辅助判断。

检测规则是上游 PHP 4.x 规则的 **1:1 移植**，并已通过**上游完整测试语料库**（32 个厂商、1749 条真实 User-Agent）验证 —— Go 移植版在 `isMobile`、`isTablet`、`version()` 以及厂商检测上与 PHP **零差异**。详见[测试与一致性](#测试与一致性)。

- 模块路径：`github.com/anhao/go-mobile-detect`
- Go 版本：1.21+
- 许可证：MIT

---

## 安装

```bash
go get github.com/anhao/go-mobile-detect
```

## 快速开始

```go
package main

import (
    "fmt"

    "github.com/anhao/go-mobile-detect"
)

func main() {
    d := mobiledetect.New()
    d.SetUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0 Mobile/15E148 Safari/604.1")

    mobile, _ := d.IsMobile() // true
    tablet, _ := d.IsTablet() // false
    isIOS, _ := d.Is("iOS")   // true

    ver, _ := d.Version("iOS", mobiledetect.VersionTypeString) // "14_0"
    v, _   := d.VersionFloat("iOS")                            // 14.0

    fmt.Println(mobile, tablet, isIOS, ver, v)
}
```

## 配合 `net/http` 使用

在 HTTP handler 中，最地道的入口是 `NewFromRequest`，它会自动从请求里提取已知的 User-Agent 类头信息和“移动正信号”头信息：

```go
func handler(w http.ResponseWriter, r *http.Request) {
    d := mobiledetect.NewFromRequest(r)
    if mobile, _ := d.IsMobile(); mobile {
        // 提供移动端体验
    }
}
```

如果你已经把头信息放在一个 `map[string]string` 里，可以用 `SetHttpHeaders`。头名匹配不区分大小写和连字符，所以 PHP 风格的 `"HTTP_USER_AGENT"` 和 Go 规范风格的 `"User-Agent"` 都能用。

## API

### 创建检测器

| 函数 | 说明 |
|---|---|
| `New()` | 默认内存缓存，默认配置。 |
| `NewWithConfig(cfg)` | 自定义 [`Config`](#配置)。 |
| `NewWithCache(cfg, cache)` | 注入你自己的 [`Cache`](#缓存)（如 Redis）。 |
| `NewFromRequest(r)` | 从 `*http.Request` 构建。 |
| `NewFromRequestWithConfig(r, cfg)` | 从请求构建并使用自定义配置。 |

### 配置

```go
type Config struct {
    AutoInitHeaders       bool          // Go 中无效（没有全局 $_SERVER）；保留是为与 PHP 对齐。默认 false。
    MaximumUserAgentLength int          // 匹配前截断 UA；0 或负值表示不截断。默认 500。
    CacheTTL              time.Duration // 检测结果的缓存有效期。默认 24h。
}
```

### 检测方法

| 方法 | 对应的 PHP 方法 | 返回值 |
|---|---|---|
| `IsMobile() (bool, error)` | `isMobile()` | 是否检测到任意移动设备 |
| `IsTablet() (bool, error)` | `isTablet()` | 是否检测到任意平板设备 |
| `Is(rule string) (bool, error)` | `is<Name>()` 魔术方法 | 指定规则是否命中（不区分大小写），如 `"iPhone"`、`"iOS"`、`"AndroidOS"`、`"Chrome"`、`"Huawei"` |
| `IsMobileNamed(rule) bool` | `is<Name>()` | 忽略错误的便捷封装 |
| `Version(prop, type) (string, bool)` | `version()` | 提取到的版本字符串；`type` 可取 `VersionTypeString` 或 `VersionTypeFloat` |
| `VersionFloat(prop) (float64, bool)` | `version(..., float)` | 以浮点数返回版本（如 `"4.3.1"` → `4.31`） |
| `Match(regex) bool` | `match()` | 用自定义正则去匹配 UA |
| `CheckHttpHeadersForMobile() bool` | `checkHttpHeadersForMobile()` | 基于头信息的快速移动信号判断 |

当尚未设置 User-Agent 时，检测方法返回 `ErrNoUserAgent`；UA 为空时直接返回 `false`（与 PHP 行为一致）。

### 访问器

`GetUserAgent`、`SetUserAgent`、`GetHttpHeaders`、`SetHttpHeaders`、`GetHttpHeader`、
`GetRules`、`GetPhoneDevices`、`GetTabletDevices`、`GetBrowsers`、`GetOperatingSystems`、
`GetProperties`、`GetVersion`、`GetCache`、`SetCache`、`MatchingRegex`、`Matches`。

### 缓存

每条检测结果都缓存在实例级的 `Cache` 里。默认是一个带 FIFO 淘汰、容量上限为 `DefaultMaxEntries`（1000）的内存缓存 —— 这能防止长驻服务在复用同一个检测器处理大量不同 UA 时出现内存无限增长。

```go
// 调整容量上限：
d := mobiledetect.NewWithConfig(mobiledetect.Config{})
d.GetCache().(*mobiledetect.MemoryCache) // 或通过 NewWithCache 传入你自己的实现
```

你也可以实现 `Cache` 接口，用 Redis、Memcached 等做后端：

```go
type Cache interface {
    Get(key string) (any, bool)
    Set(key string, value any, ttl time.Duration)
    Has(key string) bool
    Delete(key string)
    Clear()
}
```

`MemoryCache` 支持并发安全。

## 鸿蒙 HarmonyOS

鸿蒙作为**操作系统**通过 `Is("HarmonyOS")` 检测，同时覆盖两类鸿蒙设备：

- **经典鸿蒙（基于 Android）**：UA 含 `HarmonyOS` 标识，同时也带 `Android` 内核标记。
- **鸿蒙 NEXT / 纯血鸿蒙**：UA 不再含 `Android`，改用 `OpenHarmony`、`ArkWeb`（ArkWeb 内核）标识。

OS 规则会匹配 `HarmonyOS`、`OpenHarmony`、`ArkWeb` 这三类标识，因此两类设备都能被识别为鸿蒙，且都会被判定为移动设备。

```go
d := mobiledetect.New()

// 经典鸿蒙（基于 Android）
d.SetUserAgent("Mozilla/5.0 (Linux; Android 10; HarmonyOS; ALN-AL00; HMSCore 6.11.0) AppleWebKit/537.36 Mobile Safari/537.36")
d.Is("HarmonyOS") // true
d.Is("AndroidOS") // true（其 UA 仍带 Android 内核标记）
d.IsMobile()      // true

// 鸿蒙 NEXT / 纯血鸿蒙
d.SetUserAgent("Mozilla/5.0 (Phone; OpenHarmony 5.0) AppleWebKit/537.36 Safari/537.36 ArkWeb/4.1.6.1")
d.Is("HarmonyOS") // true
d.Is("AndroidOS") // false（纯血鸿蒙不再带 Android）
d.IsMobile()      // true
```

> 这是相对上游 PHP 库的增强：上游只匹配字面 `HarmonyOS`，会漏掉以 `OpenHarmony` / `ArkWeb` 标识自己的鸿蒙 NEXT 设备。

本库识别设备**类型**（手机/平板）、**操作系统**、**浏览器**和**版本**，不识别设备的品牌或具体型号。如需品牌/机型识别，请改用 HTTP Client Hints（`Sec-CH-UA*`）或专业的设备指纹库。

## 并发

和上游 PHP 类一样，`MobileDetect` 是一个**有状态、按请求**使用的对象。请在单个 goroutine 里完成配置（调用 `SetUserAgent` / `SetHttpHeaders`）；配置完成后，检测方法（`IsMobile`、`IsTablet`、`Is`、`Version`）可以并发调用。推荐的用法是**每个请求一个 `MobileDetect` 实例**（构造开销很小）。

> **编译后的正则缓存是进程级全局的**，不是按实例的——即使你每个请求新建一个检测器，每条规则在整个进程里也只编译一次。但**结果缓存默认是按实例的**；对于 UA 高度重复的高吞吐场景，请通过 `NewWithCache` 传入一个共享的 `Cache`，让结果在请求之间复用。

> **正则引擎：** 绝大多数规则用 Go 标准库 `regexp`（RE2）编译，线性时间、天然抗 ReDoS。少数需要 PCRE 专有特性（负向先行断言 `(?!...)`）的规则回退到
> [`github.com/dlclark/regexp2`](https://github.com/dlclark/regexp2)，并设有匹配超时（因为 UA 是攻击者可控输入）。

## 测试与一致性

本包包含单元测试、基准测试，以及一套基于上游完整测试数据构建的黄金语料库——**32 个厂商、1749 条真实 User-Agent**。Go 移植版在 `IsMobile`、`IsTablet`、`Version()` 以及厂商检测上与 PHP 库**零差异**。

运行全部测试（含竞态检测器）：

```bash
go test ./... -race
go test ./... -bench=. -run=^$
```

## 与 PHP 库的差异

- PHP 的 `is<Name>()` 魔术方法变成了 `Is("Name")`（不区分大小写）以及便捷封装 `IsMobileNamed("Name")`。
- `version()` 在未命中时返回 `(string, bool)` / `VersionFloat()` 返回 `(float64, bool)`，而不是 PHP 的 `false` —— 更符合 Go 习惯。
- Go 中不适用从 `$_SERVER` 自动初始化；请显式使用 `NewFromRequest` 或 `SetHttpHeaders`。
- 缓存键的生成函数固定为 SHA-1（PHP 默认值）。

## 致谢

- 原始 PHP 库：[Serban Ghita 及贡献者](https://github.com/serbanghita/Mobile-Detect) —— MIT 许可证。
- 本 Go 移植版：anhao —— MIT 许可证。

## 许可证

MIT —— 见 [LICENSE](LICENSE)。
