# Language switch UI guidance

Design owner: designer session `ses_ecf5fc561ffet4Rh2BhP09uZEw`. Implementation: OMP executor. Acceptance: supervisor.

- Public pages: append the switch to the existing navigation in `views/dealer/partials/header.html`; reuse the adjacent rounded-link classes without changing business navigation order.
- Signed-in merchant: place it between API Docs and the account/sign-out controls; reuse `admin-titlebar-link`.
- Signed-in admin: place it before Sign out; reuse `admin-titlebar-link`.
- Invoice: place it beside the header status area in `views/invoice.html`; reuse `invoice-button` styling without changing the invoice body.
- Show both language names, 简体中文 and English. Make the current choice identifiable beyond color, with accessible current-state markup and an explanatory label.
- Use keyboard-focusable GET links, outside forms. Preserve the current path and unrelated query parameters.
- On narrow screens keep both choices readable; do not shrink, hide, or reorder business actions. Use the existing wrapping/scrolling behavior where available.
- Do not change branding, colors, fonts, or the existing visual system.
- Acceptance: public, merchant, admin and invoice entrances; selected state; keyboard access and visible focus; no accidental form submission; no obstructed controls on narrow screens.
