# 真机 / Emulator 5 步验证 · 回填模板

> **用法**：你按 `handoff/2026-09-21-edge-to-edge-and-endpoint-switch-pickup.md` 跑完 5 步，把下面 8 个字段填好直接贴回 chat。我立刻做：折叠进 `runtime-evidence-summary.md` → 同步 `STATE.md` §2 表「5. 运行时 30min 后台」行 → `update_goal status: complete`。
> 
> 时间：30 秒。

---

## 1. 设备 + 系统

```
设备：        ____________________  （例：Redmi Note 14 / Pixel 8 / Galaxy S24）
Android 版本：____________________  （例：14）
ROM：         ____________________  （例：MIUI 14.0.4 / Stock AOSP / One UI 6.0）
```

## 2. 全屏背景（edge-to-edge）

```
主页面顶部有白色或黑色细横条？
  ☐ 完全没看见任何横条（PASS）
  ☐ 顶部有 1 根与主题色不同的横条（FAIL，描述：______）
  ☐ 其他：________________________________

下拉通知时，状态栏区是否变为 app 主题色？
  ☐ 是（PASS）
  ☐ 否（描述：______）

底部导航区颜色是否同样跟主题？
  ☐ 是（PASS）
  ☐ 否（描述：______）
```

## 3. 端点切换 - 看到 4 个 preset 吗？

按 ≡ → 设置 → 后端服务器：

```
能看到的选项数：
  ☐ 4 个（PASS，含「构建默认 / 当前站点(同源) / 生产环境(pocket.itestu.cn) / 备用入口(pocket.kxpms.cn) / 自定义地址」）
  ☐ 3 个或更少（FAIL，描述实际看到的：______）
  ☐ 5 个（PASS，多了一个自定义）

默认选中：
  ☐ 生产环境（pocket.itestu.cn）
  ☐ 备用入口（pocket.kxpms.cn）
  ☐ 其他：______

i18n 显示：
  ☐ 中文（生产环境（pocket.itestu.cn）/ 备用入口（pocket.kxpms.cn））
  ☐ 英文（Production (pocket.itestu.cn) / Backup (pocket.kxpms.cn)）
  ☐ 其他：______
```

## 4. 切换到 pocket.kxpms.cn

```
点「备用入口（pocket.kxpms.cn）」+ 保存并使用后：
  ☐ 自动跳到 /login 页，之前的 session 被注销（PASS）
  ☐ 没跳，在当前页有错误（描述：______）
```

```
kxpms.cn /healthz 是否可达：
  ☐ 可（PASS，能登录）
  ☐ 不可（FAIL，截图错误信息：______）
```

## 5. 切换回 pocket.itestu.cn

```
回到设置 → 切回「生产环境（pocket.itestu.cn）」+ 保存并使用后：
  ☐ 自动跳到 /login（PASS）
  ☐ 当前页（FAIL，描述：______）

itestu.cn /healthz 是否可达：
  ☐ 可（PASS，能登录）
  ☐ 不可（FAIL，描述：______）
```

## 6. 其它

```
有没有遇到闪退 / 白屏 / 卡顿？
  ☐ 没（PASS）
  ☐ 有（描述：______）

是否有任何「和之前不一样」的感觉？
  ☐ 没（PASS，体验流畅）
  ☐ 有（描述：______）
```

## 7. 你端总评

```
整体满意度（1-5）：______
推荐给别人？ ☐ 是 ☐ 否
一句话总结：__________________________________________________
```

## 8. 截图（可选）

把 4-preset 设置页 + 切换成功后的 login 页的截图保存为：
```
test-evidence\real-device-2026-09-21\04-presets.png
test-evidence\real-device-2026-09-21\05-after-switch.png
```

---

**回填给 Mavis 后**，我按 `update_goal status: complete` 收尾。

**写于**：2026-09-21 11:57 · agent 模板 / 上面 8 节直接粘贴即可
