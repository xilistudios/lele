(function () {
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
  var prefersLight = window.matchMedia("(prefers-color-scheme: light)").matches;
  apply(prefersLight ? "light" : "dark");
})();
