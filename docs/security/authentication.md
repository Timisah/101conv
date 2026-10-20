# Authentication and request security

101conv keeps administrator and account passwords compatible with the current API contract: five to 256 characters are accepted. Five characters are not a recommended security level. Use a unique, long passphrase for every administrator and account, and replace weak credentials during normal maintenance. Existing credentials are not invalidated by this policy. Passwords are stored as salted Argon2id hashes; plaintext passwords are never returned.

Initial setup and sign-in are rate limited. The limiter uses the immediate network peer by default. `X-Real-IP` is used only when the bundled web proxy has overwritten the internal `X-101conv-Proxy: web` marker; direct clients cannot rotate that header to create unlimited login buckets. If several proxies are used, the outermost trusted proxy must overwrite, rather than append, the client-address headers before passing the request to the bundled web service. Rate limiting a whole proxy is safer than trusting an unconfigured chain.

Browser session cookies are host-only, HttpOnly, and SameSite Strict. `Secure` is set for HTTPS requests identified through the bundled proxy boundary or a direct TLS connection. Core and PostgreSQL are private in the supplied Compose file; do not publish Core directly.

State-changing cookie-authenticated requests require an exact Origin or Referer match, or browser Fetch Metadata that is present only when both origin headers are absent. Guest uploads, submissions, cancellations, and logout additionally accept the frontend's `X-101conv-Request: guest-session` marker when a documented proxy strips browser metadata. JSON content type alone is not a CSRF credential. The public guest-session bootstrap endpoint remains intentionally unauthenticated and rate limited; it creates an isolated guest credential but does not authorize administrative actions.

Upload size is bounded at 1 GiB per file by the shared instance safety ceiling. Guest and account quotas are independently configurable below that ceiling, and guest storage reservations include the configured input allowance plus the 200 MiB maximum output reservation.

For an HTTPS reverse proxy, preserve the public Host, overwrite `X-Forwarded-Proto` with the externally observed scheme, and preserve the non-simple guest request marker. Never let clients supply the forwarded values used by an application. The bundled Nginx service canonicalizes Host, overwrites address headers, and marks the internal proxy hop. `X-Forwarded-Host` is not used by Core for origin or cookie decisions.
