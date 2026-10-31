# Legacy plugin host

This optional leaf package owns v1 web-plugin compatibility: local assets/RPC,
page events, theme tokens, the bounded WebView cache and resource release.
It uses the MyGo `examples/embed-webview` approach and the existing app backend.

The native desktop passes a detached `Input` snapshot through `Update`, places
`Page(id)` with `ui.WebView`, and calls `Reload` or `Close`. These calls belong
on the UI thread. The parent plugin page needs no HTTP/RPC or JS lifecycle code.
Listeners and WebViews are created only after a plugin page is first opened.

Keep plugin-only behavior here; keep native theme selection and navigation in
the parent package. The legacy JS assets are embedded under `resources/plugin`.
Integration and optional browser regression tests live beside this package.
