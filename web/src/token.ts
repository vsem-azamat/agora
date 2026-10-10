// The web token: taken from a #token= fragment once, then kept in local storage.
const KEY = 'agora.token';

/** Reads `token=<value>` from a URL fragment such as `#token=abc`. */
export function tokenFromHash(hash: string): string | undefined {
  const params = new URLSearchParams(hash.replace(/^#\/?/, ''));
  const t = params.get('token')?.trim();
  return t || undefined;
}

export function loadToken(): string | undefined {
  try {
    return localStorage.getItem(KEY) ?? undefined;
  } catch {
    return undefined;
  }
}

export function saveToken(t: string) {
  try {
    localStorage.setItem(KEY, t);
  } catch {
    // without storage the token lasts for this page only
  }
}

export function forgetToken() {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // nothing stored
  }
}

/**
 * Takes a token from the address bar, if there is one: stores it and removes it from the
 * address and the history entry, so it is not left in the URL.
 */
export function takeTokenFromLocation(): string | undefined {
  const t = tokenFromHash(location.hash);
  if (t) {
    saveToken(t);
    history.replaceState(null, '', `${location.pathname + location.search}#/`);
  }
  return t ?? loadToken();
}
