> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/governance/STATUS-MATRIX.md`](docs/governance/STATUS-MATRIX.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a point-in-time sprint report (交付/测试/部署/修复记录); 当前能力以治理矩阵为准。

# 📱 新版本已部署！包含关键修复

**修复内容**：
- ✅ 添加了连接清理逻辑
- ✅ 防止重复`createConnection`导致`lobster already exists`错误
- ✅ 在创建新连接前先关闭旧连接

---

## 📱 现在请在手机上操作

### 应该会弹出安装界面
点击 **"安装"** 按钮

### 如果没有自动弹出
1. 打开"文件管理器"
2. 进入"下载"文件夹
3. 找到 **`opencode-pocket-v4.apk`** (24MB)
4. 点击安装

---

## 🧪 安装后请测试

1. **打开应用** - 应该看到登录页
2. **登录** - admin / admin
3. **观察**:
   - 是否有lobster错误？
   - 登录是否成功？
   - 能否跳转到主页？

---

## 🐛 如果还出现错误

如果还有`lobster already exists`错误，可能是需要完全卸载后手动安装。

请告诉我测试结果！
