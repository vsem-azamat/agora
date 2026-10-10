import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './fonts/fonts.css';
import './styles.css';
import { App } from './App';
import { applyTheme, resolveTheme, storedTheme } from './theme';

applyTheme(resolveTheme(storedTheme()));

const root = document.getElementById('root');
if (!root) throw new Error('the page has no #root element');
createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
