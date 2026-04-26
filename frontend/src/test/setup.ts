// Vitest global setup. jest-dom adds custom matchers (toBeInTheDocument, etc.)
// to the expect API, used across the React component tests.
import '@testing-library/jest-dom/vitest';

// jsdom doesn't implement scrollIntoView (App.tsx auto-scrolls the log
// panel using it). Stubbing here keeps the tests focused on behaviour
// rather than juggling jsdom limitations in every file.
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}
