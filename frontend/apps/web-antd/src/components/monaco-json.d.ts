// monaco-editor 子路径（exports 通配映射到 esm/vs/）无类型映射——按运行时真实导出声明
declare module 'monaco-editor/languages/features/json/register' {
  export interface MonacoJSONDiagnosticsOptions {
    allowComments?: boolean;
    trailingComma?: 'error' | 'ignore' | 'warn';
    validate?: boolean;
  }
  export interface MonacoJSONLanguageServiceDefaults {
    setDiagnosticsOptions(options: MonacoJSONDiagnosticsOptions): void;
  }
  export const jsonDefaults: MonacoJSONLanguageServiceDefaults;
}
