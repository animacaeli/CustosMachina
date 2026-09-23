// vite 的 ?worker 导入：返回 Worker 构造器
declare module '*?worker' {
  const workerConstructor: new () => Worker;
  export default workerConstructor;
}
