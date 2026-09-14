// Apply stored theme before first paint to prevent flash.
// External (not inline) so the CSP can drop 'unsafe-inline' from script-src
// (WEB-M13). Loaded synchronously in <head> exactly where the inline version
// used to be, preserving the pre-paint timing contract.
(function () {
  // Resolve the effective theme, then apply BOTH data-theme (drives the CSS
  // custom-property switch in index.css) and the `.dark` class (drives
  // Tailwind's darkMode:'class' variants). Doing both pre-paint prevents a
  // flash: without `.dark` here, any `dark:` utility would only appear after
  // ThemeContext's post-paint useEffect runs.
  function apply(theme) {
    document.documentElement.setAttribute("data-theme", theme);
    document.documentElement.classList.toggle("dark", theme === "dark");
  }
  try {
    var stored = localStorage.getItem("lele-theme");
    if (stored === "dark" || stored === "light") {
      apply(stored);
      return;
    }
  } catch (e) {}
  // Fall back to system preference
  var prefersLight = window.matchMedia("(prefers-color-scheme: light)").matches;
  apply(prefersLight ? "light" : "dark");
})();
