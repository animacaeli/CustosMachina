import { describe, expect, it } from 'vitest';

import {
  convertView,
  convertViewWithComments,
  parseConfig,
  serializeConfig,
} from './config-format';

describe('config-format 格式互转', () => {
  const sample = { db: { host: '127.0.0.1', password: 'p' }, 'server.port': 0 };

  it('json → env：嵌套点号展开', () => {
    const env = serializeConfig('env', {
      db: { host: 'h', password: 'p' },
      server: { port: 8080 },
    });
    expect(env).toContain('db.host=h');
    expect(env).toContain('server.port=8080');
  });

  it('yaml → env / env → json 往返', () => {
    const yaml = 'db:\n  host: h\nserver:\n  port: 8080\n';
    const env = convertView('yaml', 'env', yaml);
    expect(env).toContain('db.host=h');
    expect(env).toContain('server.port=8080');
    const json = convertView('env', 'json', env ?? '');
    expect(JSON.parse(json ?? '')).toEqual({
      db: { host: 'h' },
      server: { port: 8080 },
    });
  });

  it('数字/布尔类型推断', () => {
    const obj = parseConfig('env', 'a=1\nb=true\nc=text\nd=1.5');
    expect(obj).toEqual({ a: 1, b: true, c: 'text', d: 1.5 });
  });

  it('数组：索引路径展开与回转', () => {
    const env = serializeConfig('env', { items: ['a', 'b'] });
    expect(env).toContain('items.0=a');
    expect(env).toContain('items.1=b');
    const back = parseConfig('env', env);
    expect(back).toEqual({ items: ['a', 'b'] });
  });

  it('ini：一级对象变 section', () => {
    const ini = serializeConfig('ini', {
      db: { host: 'h', port: 5432 },
      top: 1,
    });
    expect(ini).toContain('[db]');
    expect(ini).toContain('host=h');
    expect(ini).toContain('port=5432');
    expect(ini).toContain('top=1');
    expect(parseConfig('ini', ini)).toEqual({
      db: { host: 'h', port: 5432 },
      top: 1,
    });
  });

  it('toml 往返', () => {
    const toml = serializeConfig('toml', sample);
    expect(toml).toContain('[db]');
    const back = parseConfig('toml', toml);
    expect(back).toEqual(sample);
  });

  it('jsonc：注释与尾逗号容忍；字符串中的 // 不受影响', () => {
    const jsonc = `{
  // 应用配置
  "url": "https://example.com", /* 块注释 */
}`;
    expect(parseConfig('json', jsonc)).toEqual({ url: 'https://example.com' });
  });

  it('含点号的扁平 key 重建嵌套（含裸 key 不嵌套的歧义按点号处理）', () => {
    expect(parseConfig('env', 'a.b=1')).toEqual({ a: { b: 1 } });
  });

  it('换行/引号值的扁平序列化与回读', () => {
    const env = serializeConfig('env', { s: 'a\nb' });
    expect(env).toContain(`s=${JSON.stringify('a\nb')}`);
    expect(parseConfig('env', env)).toEqual({ s: 'a\nb' });
  });

  it('convertView：解析失败返回 null', () => {
    expect(convertView('yaml', 'json', 'a: [unclosed')).toBeNull();
    expect(convertView('json', 'env', '{bad json')).toBeNull();
  });

  it('同格式直通', () => {
    expect(convertView('yaml', 'yaml', 'a: 1')).toBe('a: 1');
  });
});

// ---- v0.14.2 注释感知转换（注释随键走） ----
const yamlWithComments = `# 顶部说明
mysql:
  # 数据库地址
  host: 172.28.16.8
  port: 3306
redis:
  host: redis
services:
  # 容器内监听 80
  port: 80
`;

describe('convertViewWithComments（注释随键走）', () => {
  it('yaml → json(JSONC)：注释转 // 且挂在对应键上方', () => {
    const jsonc = convertViewWithComments('yaml', 'json', yamlWithComments);
    expect(jsonc).toBeTruthy();
    expect(jsonc).toContain('// 顶部说明');
    expect(jsonc).toContain('// 数据库地址');
    expect(jsonc).toContain('// 容器内监听 80');
    // 注释归属：注释块挂在对应键上方（yaml 的 map 前置注释 → 父键上方）
    const lines = (jsonc ?? '').split('\n');
    const idx = lines.findIndex((l) => l.includes('"mysql"'));
    expect(lines[idx - 1]).toContain('数据库地址');
    // 结构完整：可被 JSONC 解析回去（strip 后合法）
    expect(() =>
      JSON.parse((jsonc ?? '').replaceAll(/\/\/[^\n]*/g, '')),
    ).not.toThrow();
  });

  it('yaml → toml：注释保持 #，嵌套对象变 [section]', () => {
    const toml = convertViewWithComments('yaml', 'toml', yamlWithComments);
    expect(toml).toContain('# 数据库地址');
    expect(toml).toContain('[mysql]');
    expect(toml).toContain('host = "172.28.16.8"');
  });

  it('yaml → env：平铺点号键 + # 注释', () => {
    const env = convertViewWithComments('yaml', 'env', yamlWithComments);
    expect(env).toContain('# 数据库地址');
    expect(env).toContain('mysql.host=172.28.16.8');
    expect(env).toContain('services.port=80');
  });

  it('yaml → ini：section + 注释', () => {
    const ini = convertViewWithComments('yaml', 'ini', yamlWithComments);
    expect(ini).toContain('[mysql]');
    expect(ini).toContain('# 容器内监听 80');
  });

  it('json(JSONC) → yaml：// 注释还原为 # 并挂回键上方', () => {
    const jsonc =
      convertViewWithComments('yaml', 'json', yamlWithComments) ?? '';
    const back = convertViewWithComments('json', 'yaml', jsonc);
    expect(back).toContain('# 顶部说明');
    expect(back).toContain('# 数据库地址');
    const lines = (back ?? '').split('\n');
    const idx = lines.findIndex((l) => l.trim() === 'mysql:');
    expect(lines[idx - 1]).toContain('数据库地址');
  });

  it('env → yaml：# 注释随平铺键重建挂到嵌套键', () => {
    const env = `# 头注释
# DB 主机
db.host=1.2.3.4
db.port=3306
`;
    const y = convertViewWithComments('env', 'yaml', env);
    expect(y).toContain('# 头注释');
    expect(y).toContain('# DB 主机');
    expect(y).toContain('host: 1.2.3.4');
  });

  it('类型保真：数字/布尔不退化为字符串（yaml→json→yaml）', () => {
    const y = 'a: 1\nb: true\nc: text\n';
    const j = convertViewWithComments('yaml', 'json', y) ?? '';
    expect(j).toContain('"a": 1');
    expect(j).toContain('"b": true');
    const back = convertViewWithComments('json', 'yaml', j);
    expect(back).toBe('a: 1\nb: true\nc: text\n');
  });

  it('同格式原样返回', () => {
    expect(convertViewWithComments('yaml', 'yaml', 'a: 1\n')).toBe('a: 1\n');
  });

  it('解析失败返回 null（语法错误 fail-closed）', () => {
    expect(convertViewWithComments('json', 'yaml', '{bad json')).toBeNull();
  });
});
