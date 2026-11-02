module github.com/halfking/pocket-opencode/backend

go 1.27.1

require (
	github.com/emersion/go-imap/v2 v2.0.0-beta.8
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6
	github.com/go-pdf/fpdf v0.9.0
	github.com/go-webauthn/webauthn v0.11.2
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/halfking/goacos v0.0.0-00010101000000-000000000000
	github.com/jackc/pgx/v5 v5.10.0
	github.com/kaixuan/identity-go v0.0.0-00010101000000-000000000000
	github.com/labstack/echo/v4 v4.15.4
	github.com/lib/pq v1.12.3
	github.com/mmcdole/gofeed v1.3.0
	github.com/pdfcpu/pdfcpu v0.11.0
	github.com/robfig/cron/v3 v3.0.1
	golang.org/x/crypto v0.53.0
	golang.org/x/image v0.27.0
	golang.org/x/net v0.56.0
	golang.org/x/text v0.38.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.54.0
)

require (
	github.com/PuerkitoBio/goquery v1.8.0 // indirect
	github.com/andybalholm/cascadia v1.3.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-webauthn/x v0.1.14 // indirect
	github.com/google/go-tpm v0.9.1 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.0 // indirect
	github.com/hhrutter/tiff v1.0.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/labstack/gommon v0.5.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mmcdole/goxpp v1.1.1-0.20240225020742-a0c311522b23 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	modernc.org/libc v1.74.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/kaixuan/identity-go => ./third_party/identity-go

// github.com/halfking/goacos 用**仓内副本**，不是已发布版本。
//
// 理由：goacos 仓里「把跨仓共享的注册策略收进 SDK」的那一版（ConfigFromEnv /
// Announce，也就是注册时机、端口口径、地址口径、注销时机这五个决定）
// **不在任何已发布 tag 里** —— v1.2.0 的 sdk/registry 只有 client.go /
// discover.go / doc.go，没有 service.go。
//
// 代价要说清楚：这份副本是**未发布**代码，goacos 发下一个 tag 之前它只存在于
// 本仓。好处是逐字节来自某个 commit（可复现）、离线可构建、漂移可被自动检出；
// 代价是 goacos 的修复不会自动流到这里。
//
// 版本号写成 v0.0.0-… 是**占位**：它表示「本地路径替换」，不表示任何一个
// 已发布版本。副本来自哪个 commit、逐文件 sha256、owner 发 tag 之后的收尾步骤，
// 都记在 third_party/goacos/PROVENANCE.md。
//
// ⚠ 这条 replace 是有先例的例外，不是惯例：acc-go 那边明确要求只用已发布 tag
// （见 acc-go/go.mod 的注释与 myvpn/scripts/tests/test_acc_vendor_matches_tag.py）。
// openpocket 之所以能例外，是因为它要的那个 API 压根还没发布；一旦发了 tag，
// 这里就该换回 go mod vendor。
//
// ⚠ 嵌套模块的 go 指令取**本仓**的（1.27.1），不取上游的（1.27.2）：
//   依赖要求更高版本时 GOTOOLCHAIN=auto **不会**自动切换工具链，
//   构建会直接失败（go: module ./dep requires go >= 1.27.2 (running go 1.27.1)）。
replace github.com/halfking/goacos => ./third_party/goacos
