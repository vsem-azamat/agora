// Signing in with the web token.
import { useState } from 'react';

export function SignIn({ onToken, rejected }: { onToken: (t: string) => void; rejected: boolean }) {
  const [value, setValue] = useState('');
  return (
    <main className="signin">
      <div className="mark">AGORA</div>
      <div className="motto">ΕΔΟΞΕ ΤΗΙ ΒΟΥΛΗΙ</div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (value.trim()) onToken(value.trim());
        }}
      >
        <label htmlFor="token">Web token</label>
        <input id="token" type="password" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} />
        <button type="submit">Enter</button>
        <p className="hint">
          {rejected ? 'The hub did not accept that token. ' : ''}Run <code>agora web token</code> on the hub's machine
          and open the address it prints, or paste the token here.
        </p>
      </form>
    </main>
  );
}
