import { describe, expect, it } from 'vitest';

import { convertView, parseConfig, serializeConfig } from './config-format';

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
