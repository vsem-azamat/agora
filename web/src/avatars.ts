// Avatars: pictures the hub serves by hash at /avatars/<hash>, only with the web token. An <img>
// cannot send the token, so each picture is fetched once and kept as a data: URL while the app is
// open; a data: URL has an opaque origin and cannot be opened as a page, so even an SVG shown
// through it runs nothing in the app's origin.
import { createContext, useContext, useEffect, useState } from 'react';

/** The smaller picture is 128 pixels a side, the larger 256. */
export type AvatarSize = 'small' | 'large';

/** Loads an avatar by hash: its data: URL, or undefined when it cannot be had. */
export type AvatarLoader = (hash: string, size: AvatarSize) => Promise<string | undefined>;

const HASH = /^[0-9a-f]{64}$/;

/** The kinds of image the hub takes as an avatar, for a file picker. */
export const AVATAR_TYPES = 'image/png,image/jpeg,image/webp,image/svg+xml';

/** The largest avatar file the hub takes, in bytes (an SVG may be at most 32 KiB). */
export const MAX_AVATAR = 256 * 1024;

/** The path an avatar is served at. */
export function avatarPath(hash: string, size: AvatarSize): string {
  return `/avatars/${hash}${size === 'small' ? '?size=128' : ''}`;
}

function dataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error);
    r.readAsDataURL(blob);
  });
}

/** A loader that fetches each avatar with the token at most once, and remembers a failure too. */
export function makeAvatarLoader(token: string, baseUrl = location.origin): AvatarLoader {
  const cache = new Map<string, Promise<string | undefined>>();
  return (hash, size) => {
    if (!HASH.test(hash)) return Promise.resolve(undefined);
    const key = `${hash} ${size}`;
    let got = cache.get(key);
    if (!got) {
      got = fetch(baseUrl + avatarPath(hash, size), { headers: { Authorization: `Bearer ${token}` } })
        .then((r) => (r.ok ? r.blob() : undefined))
        .then((b) => (b ? dataUrl(b) : undefined))
        .catch(() => undefined);
      cache.set(key, got);
    }
    return got;
  };
}

/** The app's avatar loader; without one, nobody has a picture. */
export const Avatars = createContext<AvatarLoader>(async () => undefined);

/** The data: URL of the avatar hash once it has arrived; undefined before, and without one. */
export function useAvatar(hash: string | undefined, size: AvatarSize): string | undefined {
  const load = useContext(Avatars);
  const [got, setGot] = useState<{ key: string; url?: string }>();
  const key = hash ? `${hash} ${size}` : '';
  useEffect(() => {
    if (!hash) return;
    let live = true;
    load(hash, size).then((url) => {
      if (live) setGot({ key: `${hash} ${size}`, url });
    });
    return () => {
      live = false;
    };
  }, [load, hash, size]);
  return got?.key === key ? got.url : undefined;
}
