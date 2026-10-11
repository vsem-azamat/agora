import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './fonts/fonts.css';
import './styles.css';
import { App } from './App';
import { applySize, readPref, resolveSize, SIZE_KEY } from './prefs';
import { applyTheme, resolveTheme, storedTheme } from './theme';
import { watchViewport } from './viewport';

applyTheme(resolveTheme(storedTheme()));
applySize(resolveSize(readPref(SIZE_KEY)));
watchViewport(document, window.visualViewport ?? undefined, () => {
  if (window.scrollY !== 0 || (window.visualViewport?.offsetTop ?? 0) !== 0) window.scrollTo(0, 0);
});

const root = document.getElementById('root');
if (!root) throw new Error('the page has no #root element');
createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
