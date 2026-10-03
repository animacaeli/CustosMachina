/**
 * 配置格式互转（R2 格式视图）：任意格式 → 统一对象 → 任意格式。
 *
 * 结构 ⇄ 扁平映射约定：
 * - 嵌套路径用点号连接（db.host=...）；ENV/INI 的 key 按点号重建嵌套
 * - INI：一级对象 key 变 [section]，二级变 section 内 key，更深层退化为点号 key
 * - 数组：索引即路径（items.0=a）；纯数字段对象回转为数组
 * - 扁平格式值全为字符串：回写时按数字/布尔自动推断类型
 * - 注释与键序不进中间表示——转换往返会丢注释/变顺序（UI 需提示）
 */
import { parse as tomlParse, stringify as tomlStringify } from 'smol-toml';
import YAML from 'yaml';

export type ConfigFormat = 'env' | 'ini' | 'json' | 'toml' | 'yaml';

export const CONFIG_FORMATS: ConfigFormat[] = [
  'yaml',
  'json',
  'toml',
  'ini',
  'env',
];

// ---- JSONC：字符串感知地去注释（保住值里的 "//"） ----
function stripJsonComments(text: string): string {
  let out = '';
  let i = 0;
  let inStr = false;
  while (i < text.length) {
    const ch = text[i] ?? '';
    const next = text[i + 1] ?? '';
    if (inStr) {
      out += ch;
      if (ch === '\\') {
        out += next;
        i += 2;
        continue;
      }
      if (ch === '"') inStr = false;
      i += 1;
      continue;
    }
    if (ch === '"') {
      inStr = true;
      out += ch;
      i += 1;
      continue;
    }
    if (ch === '/' && next === '/') {
      while (i < text.length && text[i] !== '\n') i += 1;
      continue;
    }
    if (ch === '/' && next === '*') {
      i += 2;
      while (i < text.length && !(text[i] === '*' && text[i + 1] === '/'))
        i += 1;
      i += 2;
      continue;
    }
    out += ch;
    i += 1;
  }
  return out;
}

// ---- 扁平工具 ----
function inferScalar(v: string): boolean | number | string {
  if (v === 'true') return true;
  if (v === 'false') return false;
  if (v !== '' && Number.isFinite(Number(v)) && !/[^\d.eE+-]/.test(v))
    return Number(v);
  return v;
}

function unquote(v: string): string {
  const t = v.trim();
  if (t.length >= 2 && t.startsWith('"') && t.endsWith('"')) {
    try {
      return JSON.parse(t) as string;
    } catch {
      return t.slice(1, -1);
    }
  }
  return t;
}

function flatLinesToTree(lines: string[]): Record<string, unknown> {
  const root: Record<string, unknown> = {};
  for (const raw of lines) {
    const line = raw.trim();
    if (!line || line.startsWith('#') || line.startsWith(';')) continue;
    const eq = line.indexOf('=');
    if (eq <= 0) continue;
    const key = line.slice(0, eq).trim();
    const val = inferScalar(unquote(line.slice(eq + 1)));
    assignPath(root, key.split('.'), val);
  }
  return numericKeysToArrays(root) as Record<string, unknown>;
}

function assignPath(
  target: Record<string, unknown>,
  path: string[],
  val: unknown,
) {
  let node: Record<string, unknown> = target;
  for (const seg of path.slice(0, -1)) {
    if (
      typeof node[seg] !== 'object' ||
      node[seg] === null ||
      Array.isArray(node[seg])
    ) {
      node[seg] = {};
    }
    node = node[seg] as Record<string, unknown>;
  }
  const last = path.at(-1);
  if (last !== undefined) node[last] = val;
}

// 所有 key 都是数字 → 转数组（递归）
function numericKeysToArrays(node: unknown): unknown {
  if (Array.isArray(node)) {
    return node.map((item) => numericKeysToArrays(item));
  }
  if (typeof node !== 'object' || node === null) return node;
  const src = node as Record<string, unknown>;
  const entries: Array<[string, unknown]> = Object.entries(src).map(
    ([k, v]) => [k, numericKeysToArrays(v)],
  );
  const keys = entries.map(([k]) => k);
  if (keys.length > 0 && keys.every((k) => /^\d+$/.test(k))) {
    return entries
      .toSorted((a, b) => Number(a[0]) - Number(b[0]))
      .map((entry) => entry[1]);
  }
  return Object.fromEntries(entries);
}

function flattenTree(
  node: unknown,
  prefix: string,
  out: Array<[string, string]>,
) {
  if (Array.isArray(node)) {
    node.forEach((v, i) => flattenTree(v, `${prefix}.${i}`, out));
    return;
  }
  if (typeof node === 'object' && node !== null) {
    for (const [k, v] of Object.entries(node)) {
      flattenTree(v, prefix ? `${prefix}.${k}` : k, out);
    }
    return;
  }
  out.push([prefix, scalarToFlat(node)]);
}

function scalarToFlat(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'string') {
    return /[\n"']/.test(v) ? JSON.stringify(v) : v;
  }
  return String(v);
}

// ---- parse：任意格式 → 对象 ----
export function parseConfig(fmt: ConfigFormat, text: string): unknown {
  switch (fmt) {
    case 'yaml': {
      return YAML.parse(text);
    }
    case 'json': {
      // JSONC：去注释 + 尾逗号容忍（与 Monaco 诊断行为对齐）
      const stripped = stripJsonComments(text).replaceAll(/,(\s*[\]}])/g, '$1');
      return JSON.parse(stripped);
    }
    case 'toml': {
      return tomlParse(text);
    }
    case 'env': {
      return flatLinesToTree(text.split('\n'));
    }
    case 'ini': {
      // [section] 前缀并入点号路径；section 外的行进根
      const lines: string[] = [];
      let section = '';
      for (const raw of text.split('\n')) {
        const line = raw.trim();
        const m = line.match(/^\[(.+)\]$/);
        if (m && m[1]) {
          section = m[1].trim();
          continue;
        }
        if (!line || line.startsWith('#') || line.startsWith(';')) continue;
        lines.push(section ? `${section}.${line}` : line);
      }
      return flatLinesToTree(lines);
    }
  }
}

// ---- serialize：对象 → 任意格式 ----
export function serializeConfig(fmt: ConfigFormat, obj: unknown): string {
  switch (fmt) {
    case 'yaml': {
      return YAML.stringify(obj);
    }
    case 'json': {
      return JSON.stringify(obj, null, 2);
    }
    case 'toml': {
      return tomlStringify(obj as never);
    }
    case 'env': {
      const out: Array<[string, string]> = [];
      flattenTree(obj, '', out);
      return out.map(([k, v]) => `${k}=${v}`).join('\n');
    }
    case 'ini': {
      // 一级对象 key 变 section；数组/标量/更深嵌套直接平铺（点号 key）
      const root = obj as Record<string, unknown>;
      if (typeof root !== 'object' || root === null) {
        const out: Array<[string, string]> = [];
        flattenTree(obj, '', out);
        return out.map(([k, v]) => `${k}=${v}`).join('\n');
      }
      // INI 无"退出 section"语法：根级标量与 section 混排有歧义——
      // 序列化固定为 根级标量在前、section 在后（自往返一致）
      const sections: string[] = [];
      const plain: Array<[string, string]> = [];
      for (const [k, v] of Object.entries(root)) {
        if (v !== null && typeof v === 'object' && !Array.isArray(v)) {
          const inner: Array<[string, string]> = [];
          flattenTree(v, '', inner);
          sections.push(
            `[${k}]`,
            ...inner.map(([ik, iv]) => `${ik}=${iv}`),
            '',
          );
        } else {
          flattenTree(v, k, plain);
        }
      }
      const head = plain.map(([k, v]) => `${k}=${v}`);
      return [...head, ...sections].join('\n').trimEnd();
    }
  }
}

/** 视图转换：原格式文本 → 目标格式文本；失败返回 null */
export function convertView(
  from: ConfigFormat,
  to: ConfigFormat,
  text: string,
): null | string {
  if (from === to) return text;
  try {
    const obj = parseConfig(from, text);
    if (obj === null || obj === undefined) return null;
    return `${serializeConfig(to, obj)}\n`;
  } catch {
    return null;
  }
}
