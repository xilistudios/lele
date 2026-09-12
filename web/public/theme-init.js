// Apply stored theme before first paint to prevent flash.
// External (not inline) so the CSP can drop 'unsafe-inline' from script-src
// (WEB-M13). Loaded synchronously in <head> exactly where the inline version
// used to be, preserving the pre-paint timing contract.
(function () {
  try {
    var theme = localStorage.getItem("lele-theme");
    if (theme === "dark" || theme === "light") {
      document.documentElement.setAttribute("data-theme", theme);
      return;
    }
  } catch (e) {}
  // Fall back to system preference
  var prefersLight = window.matchMedia("(prefers-color-scheme: light)").matches;
  document.documentElement.setAttribute("data-theme", prefersLight ? "light" : "dark");
})();
