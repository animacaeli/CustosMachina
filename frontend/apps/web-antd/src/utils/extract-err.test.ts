import { describe, expect, it } from 'vitest';

import { extractErrMsg } from './extract-err';

describe('extractErrMsg', () => {
  it('优先取后端 message（axios 形态）', () => {
    const e = {
      response: { data: { message: '项目名含非法字符' } },
      message: 'Request failed with status code 400',
    };
    expect(extractErrMsg(e)).toBe('项目名含非法字符');
  });

  it('无响应体时退到 error.message', () => {
    const e = { message: 'Network Error' };
    expect(extractErrMsg(e)).toBe('Network Error');
  });

  it('全部缺失时用调用方兜底文案', () => {
    expect(extractErrMsg({}, '保存失败')).toBe('保存失败');
    expect(extractErrMsg(null, '保存失败')).toBe('保存失败');
  });

  it('后端 message 为空串时不算命中（走下一层）', () => {
    const e = {
      response: { data: { message: '' } },
      message: 'fallback',
    };
    expect(extractErrMsg(e)).toBe('fallback');
  });
});
