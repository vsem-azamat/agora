import { afterEach, describe, expect, it, vi } from 'vitest';
import { avatarPath, makeAvatarLoader } from './avatars';

afterEach(() => vi.unstubAllGlobals());

const HASH = 'a'.repeat(64);

describe('avatars', () => {
  it('fetches each picture once, with the token, as a data URL', async () => {
    const fetch = vi.fn(
      async () => new Response(new Uint8Array([137, 80, 78, 71]), { headers: { 'Content-Type': 'image/png' } }),
    );
    vi.stubGlobal('fetch', fetch);
    const load = makeAvatarLoader('good-token', 'http://hub');
    const [a, b] = await Promise.all([load(HASH, 'small'), load(HASH, 'small')]);
    expect(a).toBe('data:image/png;base64,iVBORw==');
    expect(b).toBe(a);
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch).toHaveBeenCalledWith(`http://hub/avatars/${HASH}?size=128`, {
      headers: { Authorization: 'Bearer good-token' },
    });
    await load(HASH, 'large');
    expect(fetch).toHaveBeenLastCalledWith(`http://hub/avatars/${HASH}`, expect.anything());
  });

  it('has no picture when the hub has none, and asks only once', async () => {
    const fetch = vi.fn(async () => new Response('not found', { status: 404 }));
    vi.stubGlobal('fetch', fetch);
    const load = makeAvatarLoader('good-token', 'http://hub');
    expect(await load(HASH, 'small')).toBeUndefined();
    expect(await load(HASH, 'small')).toBeUndefined();
    expect(fetch).toHaveBeenCalledOnce();
  });

  it('fetches nothing for what is not a hash', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    expect(await makeAvatarLoader('t')('../index.html', 'small')).toBeUndefined();
    expect(fetch).not.toHaveBeenCalled();
    expect(avatarPath(HASH, 'large')).toBe(`/avatars/${HASH}`);
  });
});
