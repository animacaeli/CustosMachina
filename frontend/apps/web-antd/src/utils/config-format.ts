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

// ---- v0.14.2 注释感知转换（注释随键走） ----
// 中间表示 CDoc/CNode：有序键值树 + 每键注释块。解析时注释行归属其后首个
// 非注释行的键（文件头注释归根）；序列化时各格式用自身注释语法输出在键上方
// （toml/ini/env 用 #，json/jsonc 用 // —— JSON 视图即 JSONC）。
// 边界：数组元素内注释与行尾注释不迁移；往返注释保留但空行/缩进细节会变。

interface CNode {
  key: string;
  comments: string[];
  /** 叶子标量（children 为空时有效） */
  value?: unknown;
  children?: CNode[];
}

interface CDoc {
  headComments: string[];
  root: CNode[];
}

function newCNode(key: string): CNode {
  return { key, comments: [] };
}

// ---- 各格式 → CDoc ----

// yaml：parseDocument AST 保留键序与键注释（yaml v2：键前置注释块挂在
// Pair.value.commentBefore）；顶部注释 AST 落点不稳定（toString 可还原但
// 字段随版本漂移），改用行扫描兜底收集
function yamlToCDoc(text: string): CDoc {
  const headComments: string[] = [];
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (!line) continue;
    if (!line.startsWith('#')) break;
    headComments.push(line.replace(/^#\s?/, '').trimEnd());
  }
  const doc = YAML.parseDocument(text);
  const walkPairs = (items: any[]): CNode[] =>
    items
      .filter((p) => p?.key?.value !== undefined)
      .map((p) => {
        const n = newCNode(String(p.key.value));
        const before = p.value?.commentBefore ?? p.key?.commentBefore ?? '';
        n.comments = String(before)
          .split('\n')
          .map((l: string) => l.replace(/^#?\s?/, '').trimEnd())
          .filter(Boolean);
        const v = p.value;
        if (v?.items && v.items[0]?.key !== undefined) {
          n.children = walkPairs(v.items); // YAMLMap（第一项是 Pair）
        } else if (v?.items) {
          n.value = v.items.map((it: any) =>
            it.value === undefined ? YAML.parse(String(it).trim()) : it.value,
          );
        } else {
          n.value = v?.value;
        }
        return n;
      });
  return {
    headComments,
    root: walkPairs(((doc.contents as any)?.items ?? []) as any[]),
  };
}

// 行式格式（toml/ini/env）共用扫描：注释行缓冲 → 挂到下一非注释行所属键
interface FlatEntry {
  path: string;
  value: unknown;
  comments: string[];
}

function lineScan(
  text: string,
  opts: { sectionAware: boolean; commentPrefixes: string[] },
): { entries: FlatEntry[]; head: string[]; inlineArrays: boolean } {
  const entries: FlatEntry[] = [];
  const head: string[] = [];
  let pending: string[] = [];
  let section = '';
  let sawContent = false;
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (!line) continue;
    const isComment = opts.commentPrefixes.some((p) => line.startsWith(p));
    if (isComment) {
      const c = line.replace(/^[#;]\s?/, '').trimEnd();
      if (sawContent) {
        pending.push(c);
      } else {
        head.push(c);
      }
      continue;
    }
    sawContent = true;
    const sec = line.match(/^\[(.+)\]$/);
    if (sec && opts.sectionAware) {
      section = (sec[1] ?? '').trim();
      pending = [];
      continue;
    }
    const eq = line.indexOf('=');
    if (eq <= 0) {
      pending = [];
      continue;
    }
    const key = line.slice(0, eq).trim();
    const rawVal = line.slice(eq + 1).trim();
    // 标量数组 [a, b, 1]（toml/env 常见形态）
    const value: unknown = /^\[.*\]$/.test(rawVal)
      ? rawVal
          .slice(1, -1)
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean)
          .map((s) => inferScalar(unquote(s)))
      : inferScalar(unquote(rawVal));
    entries.push({
      path: section ? `${section}.${key}` : key,
      value,
      comments: pending,
    });
    pending = [];
  }
  return { entries, head, inlineArrays: true };
}

function flatToCDoc(flat: { entries: FlatEntry[]; head: string[] }): CDoc {
  const root: CNode[] = [];
  const findOrCreate = (nodes: CNode[], key: string): CNode => {
    for (const n of nodes) if (n.key === key) return n;
    const n = newCNode(key);
    nodes.push(n);
    return n;
  };
  for (const e of flat.entries) {
    const segs = e.path.split('.');
    let nodes = root;
    let node: CNode | undefined;
    for (const seg of segs) {
      node = findOrCreate(nodes, seg);
      node.children ??= [];
      nodes = node.children;
    }
    if (node) {
      // 叶子：抹掉占位 children（后建同路径覆盖先建）
      node.value = e.value;
      node.comments = e.comments;
    }
  }
  // 清理：value 已设且 children 全空的节点去掉 children
  const prune = (nodes: CNode[]) => {
    for (const n of nodes) {
      if (n.children && (n.value !== undefined || n.children.length === 0)) {
        if (
          n.children.every((c) => c.value === undefined && !c.children?.length)
        ) {
          delete n.children;
        } else {
          delete n.value;
          prune(n.children);
        }
      } else if (n.children) {
        prune(n.children);
      }
    }
  };
  prune(root);
  return { headComments: flat.head, root };
}

// jsonc：注释行收集 + 栈路径重建（标准 2 空格缩进或常规手写形态）
function jsonToCDoc(text: string): CDoc {
  const stripped = stripJsonComments(text).replaceAll(/,(\s*[\]}])/g, '$1');
  const obj = JSON.parse(stripped);
  // 注释收集：行扫描（字符串感知简版——引号内的 // 不当注释）
  const comments: Record<string, string[]> = {};
  const stack: string[] = [];
  let pending: string[] = [];
  const head: string[] = [];
  let sawKey = false;
  for (const raw of text.split('\n')) {
    let line = raw.trim();
    // 去掉行内字符串部分后再判注释，避免 URL "https://..." 误伤
    const noStr = line.replaceAll(/"(?:[^"\\]|\\.)*"/g, '""');
    const ci = noStr.indexOf('//');
    if (ci !== -1) {
      const c = line.slice(ci + 2).trim();
      line = line.slice(0, ci).trim();
      const bucket = sawKey || stack.length > 0 ? pending : head;
      if (c) bucket.push(c);
    }
    if (!line) continue;
    for (const ch of line) {
      if (ch === '{' || ch === '[') {
        // 进入对象：下一行的键属于这一层——先把 pending 挂到即将出现的键
        continue;
      }
      if (ch === '}' || ch === ']') {
        stack.pop();
        continue;
      }
    }
    const km = line.match(/^"((?:[^"\\]|\\.)*)"\s*:/);
    if (km) {
      const key = km[1] ?? '';
      const path = [...stack, key].join('.');
      if (pending.length > 0) {
        comments[path] = pending;
        pending = [];
      }
      sawKey = true;
      // 值为对象开头时入栈
      if (/:\s*\{\s*$/.test(line) || /:\s*\[\s*\{\s*$/.test(line))
        stack.push(key);
      continue;
    }
    // 独立对象收尾行（} 或 ]）：已在上面 pop；对象结尾同时携带下一键的场景由下一行处理
  }
  // 深度重建：valueToCNodes 只支持一层 comments map——改为全路径映射递归
  const buildNodes = (src: unknown, prefix: string): CNode[] => {
    const obj2 = src as Record<string, unknown>;
    if (typeof obj2 !== 'object' || obj2 === null || Array.isArray(obj2))
      return [];
    return Object.entries(obj2).map(([k, v]) => {
      const n = newCNode(k);
      n.comments = comments[`${prefix}${k}`] ?? [];
      if (v !== null && typeof v === 'object' && !Array.isArray(v)) {
        n.children = buildNodes(v, `${prefix}${k}.`);
      } else {
        n.value = v;
      }
      return n;
    });
  };
  return { headComments: head, root: buildNodes(obj, '') };
}

function parseToCDoc(fmt: ConfigFormat, text: string): CDoc {
  switch (fmt) {
    case 'yaml': {
      return yamlToCDoc(text);
    }
    case 'json': {
      return jsonToCDoc(text);
    }
    case 'toml': {
      return flatToCDoc(
        lineScan(text, { sectionAware: true, commentPrefixes: ['#'] }),
      );
    }
    case 'ini': {
      return flatToCDoc(
        lineScan(text, { sectionAware: true, commentPrefixes: ['#', ';'] }),
      );
    }
    case 'env': {
      return flatToCDoc(
        lineScan(text, { sectionAware: false, commentPrefixes: ['#'] }),
      );
    }
  }
}

// ---- CDoc → 各格式 ----

function yamlScalar(v: unknown): string {
  return YAML.stringify(v, { lineWidth: 0 }).trim();
}

function cDocToYaml(doc: CDoc): string {
  const lines: string[] = [];
  for (const c of doc.headComments) lines.push(`# ${c}`);
  const emit = (nodes: CNode[], depth: number) => {
    for (const n of nodes) {
      for (const c of n.comments) lines.push(`${'  '.repeat(depth)}# ${c}`);
      const indent = '  '.repeat(depth);
      if (n.children && n.children.length > 0) {
        lines.push(`${indent}${n.key}:`);
        emit(n.children, depth + 1);
      } else if (Array.isArray(n.value)) {
        lines.push(`${indent}${n.key}:`);
        for (const item of n.value)
          lines.push(`${indent}  - ${yamlScalar(item)}`);
      } else {
        lines.push(`${indent}${n.key}: ${yamlScalar(n.value)}`);
      }
    }
  };
  emit(doc.root, 0);
  return `${lines.join('\n')}\n`;
}

function cDocToJsonc(doc: CDoc): string {
  const lines: string[] = [];
  const headDepth = 0;
  for (const c of doc.headComments)
    lines.push(`${'  '.repeat(headDepth)}// ${c}`);
  lines.push('{');
  const emit = (nodes: CNode[], depth: number) => {
    const pad = '  '.repeat(depth);
    nodes.forEach((n, i) => {
      for (const c of n.comments) lines.push(`${pad}  // ${c}`);
      const comma = i === nodes.length - 1 ? '' : ',';
      if (n.children && n.children.length > 0) {
        lines.push(`${pad}  "${n.key}": {`);
        emit(n.children, depth + 1);
        lines.push(`${pad}  }${comma}`);
      } else {
        lines.push(
          `${pad}  "${n.key}": ${JSON.stringify(n.value ?? null)}${comma}`,
        );
      }
    });
  };
  emit(doc.root, 0);
  lines.push('}');
  return `${lines.join('\n')}\n`;
}

function tomlScalar(v: unknown): string {
  if (Array.isArray(v)) {
    return `[${v.map((x) => tomlScalar(x)).join(', ')}]`;
  }
  if (typeof v === 'string') return JSON.stringify(v);
  return String(v);
}

function cDocToToml(doc: CDoc): string {
  const lines: string[] = [];
  for (const c of doc.headComments) lines.push(`# ${c}`);
  const emitTable = (nodes: CNode[], path: string) => {
    const scalars = nodes.filter((n) => !n.children?.length);
    const tables = nodes.filter((n) => n.children?.length);
    for (const n of scalars) {
      for (const c of n.comments) lines.push(`# ${c}`);
      const v = tomlScalar(n.value);
      lines.push(`${n.key} = ${v}`);
    }
    for (const n of tables) {
      lines.push('');
      for (const c of n.comments) lines.push(`# ${c}`);
      lines.push(`[${path}${path ? '.' : ''}${n.key}]`);
      emitTable(n.children ?? [], `${path}${path ? '.' : ''}${n.key}`);
    }
  };
  emitTable(doc.root, '');
  return `${lines.join('\n')}\n`;
}

function cDocToIni(doc: CDoc): string {
  const lines: string[] = [];
  for (const c of doc.headComments) lines.push(`# ${c}`);
  const scalars = doc.root.filter((n) => !n.children?.length);
  const sections = doc.root.filter((n) => n.children?.length);
  for (const n of scalars) {
    for (const c of n.comments) lines.push(`# ${c}`);
    lines.push(`${n.key}=${scalarToFlat(n.value)}`);
  }
  const emit = (n: CNode, prefix: string) => {
    const name = `${prefix}${n.key}`;
    for (const c of n.comments) lines.push(`# ${c}`);
    lines.push('', `[${name}]`);
    const leaf = (nodes: CNode[]) => {
      for (const c of nodes) {
        for (const cm of c.comments) lines.push(`# ${cm}`);
        if (c.children?.length) {
          leaf(c.children);
        } else {
          lines.push(`${c.key}=${scalarToFlat(c.value)}`);
        }
      }
    };
    leaf(n.children ?? []);
  };
  for (const n of sections) emit(n, '');
  return `${lines.join('\n')}\n`;
}

function cDocToEnv(doc: CDoc): string {
  const lines: string[] = [];
  for (const c of doc.headComments) lines.push(`# ${c}`);
  const emit = (
    nodes: CNode[],
    prefix: string,
    parentComments: string[] = [],
  ) => {
    let firstLeaf = true;
    for (const n of nodes) {
      const path = `${prefix}${n.key}`;
      if (n.children?.length) {
        // 父节点注释下放到其首个叶子（扁平格式无父键可挂）
        emit(
          n.children,
          `${path}.`,
          firstLeaf ? [...parentComments, ...n.comments] : n.comments,
        );
        firstLeaf = false;
      } else {
        const comments = firstLeaf
          ? [...parentComments, ...n.comments]
          : n.comments;
        for (const c of comments) lines.push(`# ${c}`);
        firstLeaf = false;
        if (Array.isArray(n.value)) {
          n.value.forEach((v, i) =>
            lines.push(`${path}.${i}=${scalarToFlat(v)}`),
          );
        } else {
          lines.push(`${path}=${scalarToFlat(n.value)}`);
        }
      }
    }
  };
  emit(doc.root, '');
  return `${lines.join('\n')}\n`;
}

function serializeCDoc(fmt: ConfigFormat, doc: CDoc): string {
  switch (fmt) {
    case 'yaml': {
      return cDocToYaml(doc);
    }
    case 'json': {
      return cDocToJsonc(doc);
    }
    case 'toml': {
      return cDocToToml(doc);
    }
    case 'ini': {
      return cDocToIni(doc);
    }
    case 'env': {
      return cDocToEnv(doc);
    }
  }
}

/** 视图转换（v0.14.2 注释感知）：注释随键迁移到目标格式；失败返回 null。
 * json 视图输出 JSONC（// 注释，Monaco 以 jsonc 高亮）。 */
export function convertViewWithComments(
  from: ConfigFormat,
  to: ConfigFormat,
  text: string,
): null | string {
  if (from === to) return text;
  try {
    return serializeCDoc(to, parseToCDoc(from, text));
  } catch {
    return null;
  }
}
