(function () {
  const AUTH_USER_KEY = "conv_user_id";
  const ANON_USER_KEY = "conv_anon_user_id";

  let currentUserId = "";
  let authenticated = false;

  function sanitizeUserId(raw) {
    return (raw || "").trim().toLowerCase();
  }

  function randomSuffix() {
    if (window.crypto && window.crypto.randomUUID) {
      return window.crypto.randomUUID().replace(/-/g, "").slice(0, 12);
    }
    return Math.random().toString(36).slice(2, 14);
  }

  function getAnonymousUserId() {
    try {
      let anon = sanitizeUserId(sessionStorage.getItem(ANON_USER_KEY));
      if (!anon) {
        anon = `anonymous_${randomSuffix()}`;
        sessionStorage.setItem(ANON_USER_KEY, anon);
      }
      return anon;
    } catch (e) {
      return `anonymous_${randomSuffix()}`;
    }
  }

  function setAuthenticatedUser(userId) {
    const normalized = sanitizeUserId(userId);
    authenticated = true;
    currentUserId = normalized;
    try {
      localStorage.setItem(AUTH_USER_KEY, normalized);
    } catch (e) {
      // ignore storage errors
    }
    return normalized;
  }

  function setAnonymousUser() {
    authenticated = false;
    currentUserId = getAnonymousUserId();
    return currentUserId;
  }

  function getUserId() {
    if (currentUserId) {
      return currentUserId;
    }

    try {
      const cached = sanitizeUserId(localStorage.getItem(AUTH_USER_KEY));
      if (cached) {
        authenticated = true;
        currentUserId = cached;
        return currentUserId;
      }
    } catch (e) {
      // ignore
    }

    return setAnonymousUser();
  }

  function setUserId(userId) {
    return setAuthenticatedUser(userId);
  }

  function withUser(url) {
    const parsed = new URL(url, window.location.origin);
    parsed.searchParams.set("userId", getUserId());
    return parsed.pathname + parsed.search;
  }

  function applyUserToFormData(formData) {
    formData.set("userId", getUserId());
  }

  function loginUrl() {
    const next = window.location.pathname + window.location.search;
    return `/html/login.html?next=${encodeURIComponent(next)}`;
  }

  function resolveCurrentUserSync() {
    if (window.location.pathname.endsWith("/html/login.html") || window.location.pathname === "/html/login.html") {
      return;
    }

    try {
      const xhr = new XMLHttpRequest();
      xhr.open("GET", "/api/auth/me", false);
      xhr.send(null);

      if (xhr.status >= 200 && xhr.status < 300) {
        const payload = JSON.parse(xhr.responseText || "{}");
        if (payload && payload.ok && payload.authenticated && payload.userId) {
          setAuthenticatedUser(payload.userId);
          return;
        }
      }
    } catch (e) {
      // ignore and fall back to anonymous
    }

    setAnonymousUser();
  }

  function injectUserBar() {
    if (!document.body) {
      return;
    }

    const bar = document.createElement("div");
    bar.className = "user-auth-bar";

    const label = document.createElement("span");
    label.className = "user-auth-label";
    label.textContent = authenticated ? `User: ${getUserId()}` : "User: anonymous";

    const action = document.createElement("button");
    action.type = "button";
    action.className = "user-auth-logout";

    if (authenticated) {
      action.textContent = "Logout";
      action.addEventListener("click", async function () {
        try {
          await fetch("/api/auth/logout", { method: "POST" });
        } catch (e) {
          // ignore
        }
        try {
          localStorage.removeItem(AUTH_USER_KEY);
        } catch (e) {
          // ignore
        }
        setAnonymousUser();
        window.location.reload();
      });
    } else {
      action.textContent = "Login";
      action.addEventListener("click", function () {
        window.location.href = loginUrl();
      });
    }

    bar.appendChild(label);
    bar.appendChild(action);
    document.body.appendChild(bar);
  }

  function injectAuthNav() {
    const nav = document.querySelector(".nav-section");
    if (!nav) {
      return;
    }

    let item = nav.querySelector("[data-auth-nav]");
    if (!item) {
      item = document.createElement("a");
      item.className = "nav-item";
      item.setAttribute("data-auth-nav", "1");
      nav.appendChild(item);
    }

    if (authenticated) {
      item.href = "#";
      item.textContent = `User: ${getUserId()}`;
      item.classList.remove("active");
      item.addEventListener("click", function (event) {
        event.preventDefault();
      }, { once: true });
      return;
    }

    const onLoginPage = window.location.pathname.endsWith("/html/login.html") || window.location.pathname === "/html/login.html";
    item.href = loginUrl();
    item.textContent = "Login / Sign up";
    item.classList.toggle("active", onLoginPage);
  }

  resolveCurrentUserSync();

  window.convUser = {
    getUserId,
    setUserId,
    withUser,
    applyUserToFormData,
    isAuthenticated: function () {
      return authenticated;
    }
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () {
      injectAuthNav();
      injectUserBar();
    });
  } else {
    injectAuthNav();
    injectUserBar();
  }
})();
