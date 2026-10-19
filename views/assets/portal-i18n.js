/*
 * Portal i18n runtime.
 *
 * The server renders the per-request strings into a non-executing JSON element
 * (`<script type="application/json" id="portal-i18n-data">`) and every classic script on
 * the page reads them from here. The payload is read exactly once per document and cached
 * in this module: nothing is stored globally, `document.documentElement.lang` is left
 * alone and the browser preference (`navigator.language`) is never consulted — the page
 * locale comes from the server-rendered payload only.
 */
var PORTAL_I18N_ELEMENT_ID = 'portal-i18n-data';
var PORTAL_I18N_DEFAULT_LOCALE = 'zh-CN';
var portalI18NPayload = null;

// portalReadI18N returns the document payload as { locale: 'zh-CN' | 'en', strings: {...} }.
// A missing or unreadable element degrades to the default locale with no strings, so the
// callers fall back to the English source text instead of throwing.
function portalReadI18N() {
  if (portalI18NPayload) return portalI18NPayload;

  var element = null;
  if (typeof document !== 'undefined' && document.getElementById) {
    element = document.getElementById(PORTAL_I18N_ELEMENT_ID);
  }

  var parsed = null;
  if (element && typeof element.textContent === 'string') {
    try {
      parsed = JSON.parse(element.textContent);
    } catch (error) {
      parsed = null;
    }
  }

  portalI18NPayload = portalNormalizeI18N(parsed);
  return portalI18NPayload;
}

// portalNormalizeI18N accepts only the shape the bundle can produce; anything else
// (missing element, malformed JSON, unexpected JSON types) becomes the safe default.
// The locale is canonicalized to the two supported values so a malformed or spoofed
// payload can never steer display sorting, and only string-valued entries are kept.
function portalNormalizeI18N(parsed) {
  var payload = { locale: PORTAL_I18N_DEFAULT_LOCALE, strings: {} };
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return payload;
  if (parsed.locale === 'en' || parsed.locale === 'zh-CN') {
    payload.locale = parsed.locale;
  }
  var strings = parsed.strings;
  if (strings && typeof strings === 'object' && !Array.isArray(strings)) {
    var clean = {};
    for (var key in strings) {
      if (Object.prototype.hasOwnProperty.call(strings, key) && typeof strings[key] === 'string') {
        clean[key] = strings[key];
      }
    }
    payload.strings = clean;
  }
  return payload;
}

// portalT returns the text for key in the page locale and formats its %s / %d verbs with
// the remaining arguments. An unknown key — or an English page, where the bundle ships
// the English source text as the value — yields the key itself.
function portalT(key) {
  var name = String(key);
  var strings = portalReadI18N().strings;
  var text = Object.prototype.hasOwnProperty.call(strings, name) ? strings[name] : name;
  if (arguments.length < 2) return String(text);

  var args = Array.prototype.slice.call(arguments, 1);
  var index = 0;
  // Callback replacer: the returned argument text is inserted verbatim, so "$&", "$1",
  // a bare "%" or markup inside an argument can never be re-interpreted.
  return String(text).replace(/%[sd]/g, function (placeholder) {
    if (index >= args.length) return placeholder;
    var value = args[index];
    index += 1;
    return value === null || value === undefined ? '' : String(value);
  });
}

// portalLocale returns the page locale for display sorting and collation: "zh-CN" or "en".
function portalLocale() {
  return portalReadI18N().locale;
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { portalReadI18N, portalT, portalLocale };
}
