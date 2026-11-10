// Renders the interface language selector.
export function mount(container, api) {
  const root = document.createElement("div");
  root.className = "panel form-stack";

  const label = document.createElement("label");
  const title = document.createElement("span");
  title.className = "field-title";
  title.textContent = api.t("i18n.Language");

  const select = document.createElement("select");
  select.disabled = true;

  const hint = document.createElement("span");
  hint.className = "field-hint";
  hint.textContent = api.t("i18n.LanguageHint");

  label.appendChild(title);
  label.appendChild(select);
  label.appendChild(hint);
  root.appendChild(label);
  container.appendChild(root);

  api.fetchJSON("/api/plugins/i18n/locales").then(function (data) {
    const locales = data.locales || ["en"];
    select.innerHTML = "";
    for (const code of locales) {
      const option = document.createElement("option");
      option.value = code;
      option.textContent = code;
      if (code === data.locale) option.selected = true;
      select.appendChild(option);
    }
    select.disabled = false;
  }).catch(function (err) {
    select.innerHTML = "";
    const option = document.createElement("option");
    option.textContent = api.t("i18n.LanguagesUnavailable");
    select.appendChild(option);
    api.toast(String(err && err.message || err), false);
  });

  select.addEventListener("change", function () {
    select.disabled = true;
    api.postJSON("/api/plugins/i18n/locale", { locale: select.value }).then(function () {
      window.location.reload();
    }).catch(function (err) {
      select.disabled = false;
      api.toast(String(err && err.message || err), false);
    });
  });

  return function () {
    root.remove();
  };
}
