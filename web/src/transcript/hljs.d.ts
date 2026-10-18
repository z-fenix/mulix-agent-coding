// hljs.d.ts——highlight.js 子路径类型垫片：包 exports 表的 ./lib/common 子路径
// 未声明 types（types/index.d.ts 只声明主入口与 lib/languages/*），bundler 解析
// 下 strict TS 会报 TS7016。lib/common 与主入口同 API（仅预注册语言子集），类型
// 借用主入口声明。
declare module 'highlight.js/lib/common' {
  import hljs from 'highlight.js'
  export default hljs
}
