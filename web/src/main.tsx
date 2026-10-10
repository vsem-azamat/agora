import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './fonts/fonts.css';
import './styles.css';
import { App } from './App';
import { applySize, readPref, resolveSize, SIZE_KEY } from './prefs';
import { applyTheme, resolveTheme, storedTheme } from './theme';

applyTheme(resolveTheme(storedTheme()));
applySize(resolveSize(readPref(SIZE_KEY)));

const root = document.getElementById('root');
if (!root) throw new Error('the page has no #root element');
createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
