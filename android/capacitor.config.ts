// 这个目录**不是** Capacitor 项目根，只是留一个指针，避免有人误以为
// 该在这里跑 `cap add android`。
//
// 真正的 Capacitor 项目根是 `../frontend/`（原生工程生成在
// `../frontend/android/`）。原因与背景见同目录 README.md：只有
// `frontend/` 装了 @capacitor/*，在这里另起一套 npm 树必然版本漂移。
//
// 保留这个文件纯粹为了：如果有人 cd 到这里跑 npx cap，Capacitor 会
// 读到它而不是报错，从而把问题指向正确的目录。
export { default } from '../frontend/capacitor.config'
