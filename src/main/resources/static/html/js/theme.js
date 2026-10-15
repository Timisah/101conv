(function () {
  const STORAGE_KEY = "conv_theme";

  function applyTheme(theme) {
    const normalized = theme === "dark" ? "dark" : theme === "light" ? "light" : "";
    if (!normalized) {
      document.body.removeAttribute("data-theme");
      return;
    }
    document.body.setAttribute("data-theme", normalized);
  }

  function loadTheme() {
    try {
      return localStorage.getItem(STORAGE_KEY) || "";
    } catch (e) {
      return "";
    }
  }

  function saveTheme(theme) {
    try {
      if (!theme) {
        localStorage.removeItem(STORAGE_KEY);
      } else {
        localStorage.setItem(STORAGE_KEY, theme);
      }
    } catch (e) {
      // ignore storage errors
    }
  }

  function nextTheme(current) {
    if (current === "light") {
      return "dark";
    }
    if (current === "dark") {
      return "";
    }
    return "light";
  }

  function buttonLabel(theme) {
    if (theme === "light") {
      return "Theme: Light";
    }
    if (theme === "dark") {
      return "Theme: Dark";
    }
    return "Theme: Auto";
  }

  function initToggle() {
    if (!document.body) {
      return;
    }

    let theme = loadTheme();
    applyTheme(theme);

    const button = document.createElement("button");
    button.type = "button";
    button.className = "theme-toggle";
    button.textContent = buttonLabel(theme);
    button.title = "Toggle theme";
    button.addEventListener("click", function () {
      theme = nextTheme(theme);
      applyTheme(theme);
      saveTheme(theme);
      button.textContent = buttonLabel(theme);
    });
    document.body.appendChild(button);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initToggle);
  } else {
    initToggle();
  }
})();
