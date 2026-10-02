
## §4.104 给 26/26 配负控：查出一条**恒假**的证伪判据（BUG-V18），并解掉 5 个脚本的 origin 硬写

§4.103 拿到 `verify-finance-writepath.mjs` 真机 **26/26 通过**。但按一直守着的规矩，
**绿灯本身不证明判据有区分力** —— 这一节就是去证明它。

### §4.104.1 BUG-V18：证伪判定里有一条**设计上永远绿**的判据

先跑 `--sabotage=hide-cta`（把「记账」按钮从 DOM 摘掉，模拟死 CTA）：

```
11/26 通过
FAIL: 对照组 A：空输入时「记账」按钮 disabled
FAIL: 点「记账」后预览出现（解析请求走通）
FAIL: **直接查 PG** 确认真的写进去了
FAIL: POST /api/finance 非 4xx/5xx
FAIL: 删除按钮点得动 …（共 15 条红）
证伪判定：❌ 判据没抓到破坏 —— 本次证伪无效
```

**15 条判据明明抓到了破坏，脚本自己却判「证伪无效」。** 查它的期望键：

```js
: ['页面就位：快速记账输入框与「记账」按钮都存在', '点「记账」后预览出现（解析请求走通）']
```

而脚本的执行顺序是：**先测 `页面就位`，再摘按钮**。所以在 hide-cta 模式下
这条判据**必然 PASS** —— 它量的是破坏之前的状态。
`expectKey.every(...)` 于是永远 false，**无论破坏多彻底都输出「证伪无效」**。

这不是「判据不敏感」，是**恒假**：它要求一个不可能成立的条件。
（同一文件里还留着两处同类自证的注释——`*` 只剥一边、failed 是对象数组——
说明这条链已经栽过两次。）

顺带一个标签问题：那条 `页面就位：…按钮都存在` 是在 sabotage **之后**打印的，
此刻按钮已经被摘掉，日志却报 PASS 并声称「按钮都存在」。标签在说它没在说的东西。

修法两处：

1. 改名 `页面就位（sabotage 前基线）`，并**在摘除之后再测一次**按钮在不在，
   把真实状态打成可核对的现场证据：
   ```
   [sabotage 生效确认] 摘除后：btn=false（期望 false） input=true（期望 true，说明只摘了按钮）
   ```
   `btn` 不是 false 就 `exit 8` —— sabotage 没生效的话，后面的红**不能**算「判据抓到了破坏」。
2. hide-cta 的期望键换成 sabotage 真正会打坏的三条：
   `点「记账」后预览出现` / `**直接查 PG** 确认真的写进去了` / `POST /api/finance 非 4xx/5xx`。

修完复跑，两个负控都通过：

```
# hide-cta
逐条匹配：HIT «点「记账」后预览出现（解析请求走通）»  HIT «直接查 PG 确认真的写进去了»  HIT «POST /api/finance 非 4xx/5xx»
证伪判定：✅ 判据在有缺陷一侧如期失败

# swallow-create（拦掉 POST /api/finance 并回一个假的 201 —— 复刻 BUG-AC）
FAIL  ⚠️ 没出现「PG 未变却说成功」的假成功 — saidOk=true PG 1->1
逐条匹配：HIT «直接查 PG 确认真的写进去了»  HIT «⚠️ 没出现「PG 未变却说成功」的假成功»  HIT «POST /api/finance 非 4xx/5xx»
证伪判定：✅ 判据在有缺陷一侧如期失败
```

**⇒ 26/26 这个绿灯现在被证明不是恒真**：两种人为破坏下，判据都如期转红，
且 swallow-create 精确复现了 BUG-AC 的原场景——界面报「已入账」、
PG 停在 1→1，只有「直接查 PG」那条能识破。

注意修判据之后**必须复跑负控**：我只是把一条恒假判据换掉，
不验证新期望键能被命中的话，只是把恒假换成恒假。

### §4.104.2 5 个写路径脚本 origin 硬写，在当前设备上一律 exit 5

§4.102 查出设备装的是**生产 https 包**（`origin=https://localhost`），
而这批脚本第一关写死开发包：

| 脚本 | 原有断言 | CDP 端口 | API 端口 |
|---|---|---|---|
| verify-email-writepath.mjs | `origin !== 'http://localhost'` → exit 5 | `POCKET_CDP_PORT \|\| 9253` | env |
| verify-gateway-writepath.mjs | 同上 | `… \|\| 9260` | env |
| verify-marketplace-install.mjs | 同上 | `… \|\| 9250` | **写死 8088** |
| verify-bug-u.mjs | 同上 | `… \|\| 9247` | env |
| verify-bugaa-realdevice.mjs | 同上 | env | env |

用当前设备跑，它们会在**还没走到任何真正要验的判据**之前就退出——
看着像「脚本坏了」，实则是前置假设不成立。
`verify-finance-writepath.mjs` 早留了这个口子（`POCKET_EXPECT_ORIGIN`，
注释写明「做生产 https 回归时用…否则脚本会在第一关就退出」），这批漏了。

新增 `scripts/migrate-expect-origin.mjs`（机械不变量：替换数一致、
`EXPECT_ORIGIN` 恰好声明一次、`node --check` 对着**新内容**过、任一不满足则该文件不动），
改掉 5 个文件；`verify-marketplace-install.mjs` 的 `const API_PORT = 8088`
也改成 `Number(process.env.POCKET_API_PORT || 8088)`。

**头一版 TARGETS 漏了 `verify-bugaa-realdevice.mjs`**，
是迁移后的**全仓残留复查**把它捞出来的 —— 所以批量迁移之后必须再扫一遍全仓，
不能只看「我列的那几个」。

### §4.104.3 这一节没有解决什么

- `verify-task / email / gateway / marketplace / bug-u / bugaa` 六个脚本**尚未逐个实跑**，
  只是把前置条件解开了。
- 它们仍**各写各的 `adb forward`**（约 14 处硬编码 CDP 端口），
  尚未迁到 `lib/adb-cdp.mjs` 的 `tcp:0`。跑的时候要显式传 `POCKET_CDP_PORT` 避开撞端口。
- 闪卡两入口的**点击**、BUG-AX 设备侧负控、会议写入设备侧持久化，仍未做。
