const fs = require("fs");
const path = require("path");
const {
  Document, Packer, Paragraph, TextRun, Table, TableRow, TableCell,
  ImageRun, AlignmentType, LevelFormat, TableOfContents, HeadingLevel,
  BorderStyle, WidthType, ShadingType, VerticalAlign, PageNumber,
  PageBreak, Header, Footer,
} = require("docx");

const ASSETS = path.join(__dirname, "assets");
const CONTENT_WIDTH = 9360; // US Letter, 1" margins

// ---------- helpers ----------
const border = { style: BorderStyle.SINGLE, size: 1, color: "CCCCCC" };
const borders = { top: border, bottom: border, left: border, right: border };
const cellMargins = { top: 80, bottom: 80, left: 120, right: 120 };
const HEAD_FILL = "2E5A88";
const HEAD_TEXT = "FFFFFF";

function img(file, w, h, title) {
  return new Paragraph({
    alignment: AlignmentType.CENTER,
    spacing: { before: 120, after: 120 },
    children: [new ImageRun({
      type: "png",
      data: fs.readFileSync(path.join(ASSETS, file)),
      transformation: { width: w, height: h },
      altText: { title: title, description: title, name: title.replace(/[^a-zA-Z0-9]/g, "_") },
    })],
  });
}

function caption(text) {
  return new Paragraph({
    alignment: AlignmentType.CENTER,
    spacing: { after: 200 },
    children: [new TextRun({ text, italics: true, size: 18, color: "555555" })],
  });
}

function p(text, opts = {}) {
  return new Paragraph({
    spacing: { after: 120 },
    children: [new TextRun({ text, ...opts })],
  });
}

function bullet(text) {
  return new Paragraph({
    numbering: { reference: "bullets", level: 0 },
    spacing: { after: 60 },
    children: Array.isArray(text) ? text : [new TextRun(text)],
  });
}

function num(text) {
  return new Paragraph({
    numbering: { reference: "numbers", level: 0 },
    spacing: { after: 60 },
    children: Array.isArray(text) ? text : [new TextRun(text)],
  });
}

function h1(text) { return new Paragraph({ heading: HeadingLevel.HEADING_1, children: [new TextRun(text)] }); }
function h2(text) { return new Paragraph({ heading: HeadingLevel.HEADING_2, children: [new TextRun(text)] }); }

function cell(text, { w, headerCell = false, bold = false, fill } = {}) {
  const runs = (Array.isArray(text) ? text : [text]).map((t, i) =>
    new TextRun({ text: String(t), bold: headerCell || bold, color: headerCell ? HEAD_TEXT : "000000", size: 19, break: i > 0 ? 1 : undefined }));
  return new TableCell({
    borders, margins: cellMargins,
    width: { size: w, type: WidthType.DXA },
    shading: { fill: fill || (headerCell ? HEAD_FILL : "FFFFFF"), type: ShadingType.CLEAR },
    verticalAlign: VerticalAlign.CENTER,
    children: [new Paragraph({ children: runs })],
  });
}

// rows: array of arrays; widths: array summing to CONTENT_WIDTH (or given total)
function table(widths, header, rows, total = CONTENT_WIDTH) {
  const trs = [];
  trs.push(new TableRow({
    tableHeader: true,
    children: header.map((t, i) => cell(t, { w: widths[i], headerCell: true })),
  }));
  rows.forEach((r, ri) => {
    trs.push(new TableRow({
      children: r.map((t, i) => cell(t, { w: widths[i], fill: ri % 2 ? "F2F6FB" : "FFFFFF" })),
    }));
  });
  return new Table({ width: { size: total, type: WidthType.DXA }, columnWidths: widths, rows: trs });
}

function spacer() { return new Paragraph({ children: [new TextRun("")], spacing: { after: 80 } }); }

// ---------- document body ----------
const body = [];

// Title page
body.push(new Paragraph({ spacing: { before: 2400, after: 0 }, alignment: AlignmentType.CENTER,
  children: [new TextRun({ text: "Multi-Tenant Authentication & Subscription Server", bold: true, size: 48, color: "1F3A5F" })] }));
body.push(new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 200, after: 0 },
  children: [new TextRun({ text: "Technical Design Document", size: 32, color: "2E5A88" })] }));
body.push(new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 120 },
  children: [new TextRun({ text: "Built on Casdoor (Go) · OIDC/OAuth2 · WeChat Web QR + Phone OTP", size: 22, italics: true, color: "555555" })] }));
body.push(new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 1600 },
  children: [new TextRun({ text: "Version 1.0", size: 22 })] }));
body.push(new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 80 },
  children: [new TextRun({ text: "Date: 2026-05-20", size: 22 })] }));
body.push(new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 80 },
  children: [new TextRun({ text: "Status: Approved design — ready for implementation", size: 22 })] }));
body.push(new Paragraph({ children: [new PageBreak()] }));

// TOC
body.push(new Paragraph({ heading: HeadingLevel.HEADING_1, children: [new TextRun("Table of Contents")] }));
body.push(new TableOfContents("Table of Contents", { hyperlink: true, headingStyleRange: "1-2" }));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 1. Introduction
body.push(h1("1. Introduction & Scope"));
body.push(p("This document is the technical design for a self-hosted, multi-tenant authentication and authorization server. It serves multiple applications from a single central instance built by adopting Casdoor, an open-source, Go-based identity and access management (IAM) / single sign-on (SSO) platform. Users authenticate via WeChat Web QR (Open Platform) or phone one-time-password (OTP) over SMS through Casdoor's hosted login page. After login, applications receive signed JSON Web Tokens (JWTs) whose claims carry the user's plan, role, subscription status, and expiry, enabling each application to check subscription validity offline."));
body.push(h2("1.1 Goals"));
body.push(bullet("One central identity provider serving multiple applications (multi-tenant)."));
body.push(bullet("Login via WeChat Web QR and phone OTP (SMS via Twilio)."));
body.push(bullet("User management through Casdoor's admin UI and API."));
body.push(bullet("Issue tokens whose claims let applications verify subscription expiration offline."));
body.push(bullet("Reproducible, version-controlled provisioning (infrastructure-as-code, not click-ops)."));
body.push(h2("1.2 Non-Goals (v1)"));
body.push(bullet("Payment-provider integration / billing-driven auto-renewal (API exposed for a future billing service)."));
body.push(bullet("Instant sub-token-TTL revocation; offline verification accepts a bounded staleness window equal to the access-token TTL."));
body.push(bullet("WeChat surfaces other than Web QR (Official Account, Mini Program) — configurable later."));
body.push(bullet("High-availability / multi-node clustering (single-host Docker Compose for v1)."));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 2. Architecture
body.push(h1("2. System Architecture"));
body.push(p("A single Casdoor instance is the central identity provider for all applications. Applications never store passwords or talk to WeChat / Twilio directly; they delegate login to Casdoor via the OIDC authorization code flow and verify the resulting JWTs offline using Casdoor's published public key (JWKS)."));
body.push(img("01_architecture.png", 560, 314, "System architecture diagram"));
body.push(caption("Figure 1. System architecture — clients delegate to Casdoor; apps verify JWTs offline."));
body.push(h2("2.1 Technology Stack"));
body.push(table([2600, 3000, 3760],
  ["Concern", "Choice", "Rationale"],
  [
    ["Auth core / IdP", "Casdoor (Go)", "First-class WeChat & SMS-OTP, multi-tenant, OIDC/OAuth2 provider, built-in subscription engine"],
    ["Datastore", "PostgreSQL", "Reliable, easy backup; Casdoor-supported via its ORM"],
    ["Edge / TLS", "Caddy or Nginx (reverse proxy)", "TLS termination in front of Casdoor"],
    ["Packaging", "Docker Compose (single host)", "Simple to stand up and operate for v1"],
    ["Provisioning", "Casdoor Go SDK / declarative config", "Version-controlled, reproducible setup"],
    ["WeChat login", "WeChat Open Platform (Web QR)", "Scan-to-login on the hosted page"],
    ["SMS OTP", "Twilio", "Phone-number one-time passcode delivery"],
    ["Token format", "JWT-Custom (RS256)", "Embeds subscription claims for offline verification"],
  ]));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 3. Module Design
body.push(h1("3. Module Design"));
body.push(p("The system is composed of Casdoor's configured modules (used as-is) plus a small amount of custom code that we own: the bootstrap/provisioning layer and a per-application verification helper. We extend Casdoor's source only if a required claim or flow cannot be configured."));
body.push(img("02_modules.png", 560, 361, "Module diagram"));
body.push(caption("Figure 2. Module decomposition — green is configured Casdoor, red is custom code we own."));
body.push(h2("3.1 Module Responsibilities"));
body.push(table([2600, 4600, 2160],
  ["Module", "Responsibility", "Ownership"],
  [
    ["Login UI", "Hosted login page rendering WeChat QR and phone-OTP entry", "Casdoor (config)"],
    ["Identity Provider", "OIDC/OAuth2 endpoints: /authorize, /token, /jwks, introspection", "Casdoor (config)"],
    ["Provider Module", "WeChat Open Platform + Twilio SMS integrations", "Casdoor (config)"],
    ["User Management", "User CRUD, account linking, admin UI/API", "Casdoor (config)"],
    ["Subscription Engine", "Pricing / Plan / Role / Subscription state machine", "Casdoor (config)"],
    ["Token Module", "JWT-Custom claim assembly, signing with cert", "Casdoor (config)"],
    ["Bootstrap / IaC", "Provision orgs, apps, providers, plans reproducibly", "Custom"],
    ["Verification Helper", "Per-app JWT verify (JWKS) + subscription check", "Custom"],
  ]));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 4. Multi-tenant design
body.push(h1("4. Multi-Tenant Design"));
body.push(p("Casdoor's hierarchy maps directly onto our tenancy model. The Organization is the tenant boundary; it owns users, providers, and pricing/plans. An Application belongs to an organization and defines enabled login methods, redirect URLs, client credentials, and token/claim format. A User belongs to an organization and can be used across that organization's applications."));
body.push(p("v1 topology: one Organization per Application, giving each application fully isolated users and subscription rules.", { bold: true }));
body.push(img("05_multitenant.png", 600, 128, "Multi-tenant topology"));
body.push(caption("Figure 3. v1 tenancy: one Organization per Application, with a documented migration path to shared SSO."));
body.push(table([2400, 4400, 2560],
  ["Casdoor concept", "Our meaning", "Isolation"],
  [
    ["Organization", "Tenant boundary (per application in v1)", "Owns users, providers, plans"],
    ["Application", "App A, App B, ...", "Own client ID/secret, redirect URIs, token config"],
    ["User", "End user of an application", "Scoped to its organization"],
    ["Provider", "WeChat / Twilio credentials", "Per organization"],
    ["Pricing / Plan", "Subscription tiers", "Per application"],
  ]));
body.push(p("Migration path: if shared SSO users across applications become desirable, move to a one-Organization, many-Applications topology so users in the org sign in to all its apps. This is a configuration change plus user-data migration, not a redesign.", { italics: true }));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 5. Workflow design
body.push(h1("5. Workflow Design"));
body.push(h2("5.1 Login Flows"));
body.push(p("Both WeChat Web QR and phone OTP use the OIDC authorization code flow and terminate on Casdoor's hosted login page. The two methods resolve to a single identity when the same person uses both; the v1 linking rule is link-by-verified-phone."));
body.push(img("03_login_flow.png", 300, 694, "Login workflow diagram"));
body.push(caption("Figure 4. Login workflows for WeChat Web QR and phone OTP, ending in token issuance."));
body.push(new Paragraph({ children: [new PageBreak()] }));
body.push(h2("5.2 Token Validation, Refresh & Offline Subscription Check"));
body.push(p("On each protected request, the application verifies the JWT signature against Casdoor's JWKS (cached locally), checks expiry, and evaluates the subscription claims offline. When the short-lived access token expires, the app silently exchanges its refresh token for a new access token carrying fresh claims. The user stays signed in for the refresh-token lifetime (30 days) while subscription state is refreshed at most every access-token lifetime (15 minutes)."));
body.push(img("04_token_check.png", 405, 620, "Token validation and subscription check workflow"));
body.push(caption("Figure 5. Offline JWT verification, refresh, and subscription gating."));
body.push(p("Accepted trade-off: a user whose subscription expires or is cancelled mid-token retains access for up to the 15-minute access-token TTL. This is the cost of offline verification; an online check can be added at specific sensitive actions later if instant revocation is required."));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 6. Database design
body.push(h1("6. Database Design"));
body.push(p("Casdoor persists all state in PostgreSQL via its ORM. The entities below are the subset relevant to this design. Primary keys in Casdoor are the composite (owner, name); owner is the organization for most tenant-scoped entities. The diagrams are split into identity/tenancy and subscription/token halves for legibility."));
body.push(img("06a_erd_identity.png", 204, 420, "Entity-relationship diagram: identity and tenancy"));
body.push(caption("Figure 6a. Identity & tenancy entities."));
body.push(img("06b_erd_subscription.png", 289, 420, "Entity-relationship diagram: subscription and tokens"));
body.push(caption("Figure 6b. Subscription & token entities."));
body.push(h2("6.1 Key Entities"));
body.push(p("APPLICATION — registration and token configuration:", { bold: true }));
body.push(table([2800, 2200, 4360],
  ["Field", "Type", "Notes"],
  [
    ["owner, name", "string (PK)", "Composite primary key"],
    ["organization", "string (FK)", "Tenant the app belongs to"],
    ["clientId, clientSecret", "string", "OAuth2 client credentials"],
    ["redirectUris", "string[]", "Allowed OIDC redirect targets"],
    ["enabledProviders", "string[]", "WeChat, Twilio (SMS)"],
    ["tokenFormat", "enum", "JWT-Custom"],
    ["tokenFields", "string[]", "Custom claims: plan, role, subscriptionStatus, subscriptionEndTime"],
    ["accessTokenExpire", "int (min)", "15 minutes"],
    ["refreshTokenExpire", "int (min)", "30 days"],
    ["cert", "string (FK)", "Signing certificate (RS256)"],
  ]));
body.push(spacer());
body.push(p("SUBSCRIPTION — ties a user to a plan with a lifecycle state:", { bold: true }));
body.push(table([2800, 2200, 4360],
  ["Field", "Type", "Notes"],
  [
    ["owner, name", "string (PK)", "Composite primary key"],
    ["user", "string (FK)", "Subscriber"],
    ["plan", "string (FK)", "Selected plan (maps to a role)"],
    ["state", "enum", "Pending | Active | Upcoming | Suspended | Expired | Error"],
    ["startDate, endDate", "datetime", "Validity window; endDate drives subscriptionEndTime claim"],
  ]));
body.push(spacer());
body.push(p("PLAN & ROLE — entitlement mapping:", { bold: true }));
body.push(table([2800, 2200, 4360],
  ["Field", "Type", "Notes"],
  [
    ["plan.name", "string (PK)", "e.g. free, pro, pro-annual"],
    ["plan.price, period", "number / enum", "Pricing and billing period"],
    ["plan.role", "string (FK)", "Role granted while subscription active"],
    ["role.permissions", "string[]", "Permissions enforced via Casdoor enforce API"],
  ]));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 7. Protocol details
body.push(h1("7. Protocol Details"));
body.push(h2("7.1 OIDC / OAuth2 Authorization Code Flow"));
body.push(num("App redirects the browser to GET /login/oauth/authorize with response_type=code, client_id, redirect_uri, scope, and state."));
body.push(num("User authenticates on the hosted login page (WeChat QR or phone OTP)."));
body.push(num("Casdoor redirects back to redirect_uri with an authorization code and the original state."));
body.push(num("App exchanges the code at POST /api/login/oauth/access_token (code + client_id + client_secret) for an ID token, access token (JWT), and refresh token."));
body.push(num("App fetches and caches the signing public key from the JWKS endpoint to verify tokens offline."));
body.push(num("On access-token expiry, the app calls the token endpoint with grant_type=refresh_token to obtain a fresh access token."));
body.push(h2("7.2 Endpoints"));
body.push(table([3400, 2200, 3760],
  ["Endpoint", "Method", "Purpose"],
  [
    ["/login/oauth/authorize", "GET", "Start authorization code flow"],
    ["/api/login/oauth/access_token", "POST", "Exchange code or refresh token for tokens"],
    ["/.well-known/openid-configuration", "GET", "OIDC discovery document"],
    ["/.well-known/jwks.json", "GET", "Public signing keys for offline verification"],
    ["/api/login/oauth/introspect", "POST", "Optional online token introspection"],
    ["/api/logout", "POST", "End session / revoke"],
  ]));
body.push(h2("7.3 JWT Structure & Claims"));
body.push(p("Tokens use the JWT-Custom format signed with RS256. The header carries alg and kid; the signature is verified against the JWKS public key. The payload carries standard OIDC claims plus the custom subscription claims that drive offline gating."));
body.push(table([3000, 2000, 4360],
  ["Claim", "Example", "Meaning"],
  [
    ["sub", "uuid", "User identifier"],
    ["iss", "https://auth.example.com", "Issuer (Casdoor)"],
    ["aud", "App A clientId", "Audience / application"],
    ["org", "org-app-a", "Tenant organization"],
    ["iat / exp", "epoch", "Issued-at / expiry (15 min after iat)"],
    ["plan", "pro", "Active plan name"],
    ["role", "pro-role", "Role backing the plan"],
    ["subscriptionStatus", "Active", "Pending | Active | Upcoming | Suspended | Expired | Error"],
    ["subscriptionEndTime", "2026-12-31T00:00:00Z", "Expiry used for offline check"],
  ]));
body.push(p("App-side gate: verify signature against JWKS, then require subscriptionStatus == \"Active\" AND now < subscriptionEndTime.", { bold: true }));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 8. Threading & concurrency
body.push(h1("8. Threading & Concurrency Design"));
body.push(p("Casdoor is a stateless Go service: each inbound HTTP request is served on its own goroutine by the underlying HTTP server, so request concurrency scales with available CPU and the goroutine scheduler rather than a fixed thread pool. Shared state lives in PostgreSQL, accessed through a bounded connection pool. The design keeps the auth server horizontally scalable later because no per-request state is held in process beyond caches."));
body.push(table([2800, 6560],
  ["Concern", "Design / mitigation"],
  [
    ["Request concurrency", "Goroutine-per-request (Go net/http). No shared mutable request state; CPU-bound crypto (RS256 signing) parallelizes across cores."],
    ["Database access", "Bounded PostgreSQL connection pool (max-open / max-idle tuned). Transactions kept short; subscription state reads are single-row lookups."],
    ["JWKS / key handling", "Signing key loaded once and cached in memory; apps cache JWKS client-side with periodic refresh to avoid per-request fetches."],
    ["OTP issuance", "Per-phone rate limiting and short OTP TTL to bound SMS abuse and brute force; verification attempts are counted and throttled."],
    ["Refresh-token rotation", "Refresh exchange is a short serialized DB transaction; a revoked/rotated token is rejected to prevent replay under concurrent refresh."],
    ["Subscription state writes", "Lifecycle transitions (Active -> Expired, etc.) are atomic single-row updates; the next token issuance reads the committed state."],
    ["Idempotency", "OAuth authorization codes are single-use and short-lived; concurrent code redemption is rejected after first use."],
    ["Caching staleness", "Offline JWT verification accepts staleness bounded by the 15-minute access-token TTL by design (see 5.2)."],
  ]));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 9. Threat model & risk
body.push(h1("9. Threat Model & Risk Analysis"));
body.push(p("The analysis below follows the STRIDE categories against the auth server's trust boundaries: the public edge (browser/app to proxy), the auth core, the datastore, and the external providers (WeChat, Twilio)."));
body.push(h2("9.1 STRIDE Threats & Mitigations"));
body.push(table([1700, 3400, 2700, 1560],
  ["Category", "Threat / vector", "Mitigation", "Severity"],
  [
    ["Spoofing", "Stolen access token replayed against an app", "Short 15-min access TTL; RS256 signature verification; TLS everywhere", "High"],
    ["Spoofing", "OTP brute force / SIM-based phishing", "Per-phone rate limits, short OTP TTL, attempt throttling", "High"],
    ["Tampering", "Forged or altered JWT claims", "Asymmetric RS256 signing; apps verify against JWKS; never trust unsigned claims", "High"],
    ["Repudiation", "Disputed login / privilege change", "Casdoor audit logs for auth and admin actions; retain logs", "Medium"],
    ["Info disclosure", "Leak of client secrets / signing key / DB creds", "Secrets via env/secret files (never committed); least-privilege DB user; cert rotation", "High"],
    ["Info disclosure", "PII exposure (phone, WeChat openid)", "TLS in transit; restrict admin access with MFA; encrypt backups", "Medium"],
    ["DoS", "Authorization / token endpoint flooding", "Rate limiting at reverse proxy; connection-pool bounds; stateless scale-out path", "Medium"],
    ["Elevation", "Privilege gain via stale subscription claim", "Bounded 15-min staleness; online check for sensitive actions if needed", "Medium"],
    ["Elevation", "Account-linking confusion across methods", "Link only by verified phone; explicit linking rules", "Medium"],
  ]));
body.push(h2("9.2 Risk Register"));
body.push(table([3000, 1500, 1500, 3360],
  ["Risk", "Likelihood", "Impact", "Mitigation / owner action"],
  [
    ["Signing key compromise", "Low", "Critical", "Dedicated cert, restricted access, rotation plan, revoke + reissue procedure"],
    ["Casdoor CVE / upstream bug", "Medium", "High", "Pin versions, watch advisories, patch cadence, staging before prod"],
    ["Single-host outage (no HA in v1)", "Medium", "High", "Backups + documented restore; HA topology is a planned follow-up"],
    ["SMS provider outage (Twilio)", "Low", "Medium", "WeChat remains available; provider is pluggable; alerting on send failures"],
    ["Subscription-claim staleness abuse", "Low", "Medium", "Short TTL; add online check at high-value actions if warranted"],
    ["Misconfiguration during provisioning", "Medium", "Medium", "Version-controlled IaC bootstrap; integration tests assert config"],
  ]));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 10. Deployment & ops
body.push(h1("10. Deployment & Operations"));
body.push(bullet("Docker Compose on a single host running two services: casdoor and postgres, behind a reverse proxy (Caddy/Nginx) for TLS."));
body.push(bullet("Secrets (WeChat appid/secret, Twilio keys, JWT signing cert, DB credentials) supplied via environment or secret files; never committed to the repository."));
body.push(bullet("The JWT signing certificate is the root of trust: generate a dedicated cert and plan rotation."));
body.push(bullet("Regular PostgreSQL backups; encrypted at rest where possible."));
body.push(bullet("Admin accounts protected with MFA (Casdoor supports TOTP)."));
body.push(h2("10.1 Testing Strategy"));
body.push(bullet("Stand up the Compose stack, run the bootstrap layer, and assert an application can complete the OIDC login flow (WeChat/Twilio providers mocked/stubbed)."));
body.push(bullet("Assert issued tokens carry correct subscription claims (plan, role, subscriptionStatus, subscriptionEndTime)."));
body.push(bullet("Assert the verification helper accepts an active subscription and rejects an expired or suspended one."));
body.push(bullet("Assert refresh-token rotation works and a revoked refresh token is rejected."));
body.push(new Paragraph({ children: [new PageBreak()] }));

// 11. Open decisions
body.push(h1("11. Open Decisions (Defaulted)"));
body.push(table([3000, 3200, 3160],
  ["Decision", "Default chosen", "Revisit when"],
  [
    ["Tenancy topology", "One organization per application", "Shared SSO across apps is needed"],
    ["Access token TTL", "15 minutes", "Faster revocation or fewer refreshes desired"],
    ["Refresh token TTL", "30 days", "Different session-length policy required"],
    ["Account-linking rule", "Link by verified phone", "Additional identity signals introduced"],
  ]));
body.push(h1("12. References"));
body.push(bullet([new TextRun("Casdoor subscription: https://casdoor.org/docs/pricing/subscription/")]));
body.push(bullet([new TextRun("Casdoor plan/pricing: https://casdoor.org/docs/pricing/plan/")]));
body.push(bullet([new TextRun("Casdoor token formats (JWT-Custom): https://casdoor.org/docs/token/overview/")]));
body.push(bullet([new TextRun("Source spec: docs/superpowers/specs/2026-05-20-auth-server-design.md")]));

// ---------- assemble ----------
const doc = new Document({
  styles: {
    default: { document: { run: { font: "Arial", size: 22 } } },
    paragraphStyles: [
      { id: "Heading1", name: "Heading 1", basedOn: "Normal", next: "Normal", quickFormat: true,
        run: { size: 30, bold: true, font: "Arial", color: "1F3A5F" },
        paragraph: { spacing: { before: 280, after: 160 }, outlineLevel: 0 } },
      { id: "Heading2", name: "Heading 2", basedOn: "Normal", next: "Normal", quickFormat: true,
        run: { size: 25, bold: true, font: "Arial", color: "2E5A88" },
        paragraph: { spacing: { before: 200, after: 120 }, outlineLevel: 1 } },
    ],
  },
  numbering: {
    config: [
      { reference: "bullets", levels: [{ level: 0, format: LevelFormat.BULLET, text: "•", alignment: AlignmentType.LEFT,
        style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
      { reference: "numbers", levels: [{ level: 0, format: LevelFormat.DECIMAL, text: "%1.", alignment: AlignmentType.LEFT,
        style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
    ],
  },
  sections: [{
    properties: { page: { size: { width: 12240, height: 15840 }, margin: { top: 1440, right: 1440, bottom: 1440, left: 1440 } } },
    headers: { default: new Header({ children: [new Paragraph({ alignment: AlignmentType.RIGHT,
      children: [new TextRun({ text: "Auth Server — Technical Design", size: 16, color: "888888" })] })] }) },
    footers: { default: new Footer({ children: [new Paragraph({ alignment: AlignmentType.CENTER,
      children: [new TextRun({ text: "Page ", size: 16, color: "888888" }), new TextRun({ children: [PageNumber.CURRENT], size: 16, color: "888888" }),
        new TextRun({ text: " of ", size: 16, color: "888888" }), new TextRun({ children: [PageNumber.TOTAL_PAGES], size: 16, color: "888888" })] })] }) },
    children: body,
  }],
});

Packer.toBuffer(doc).then((buf) => {
  const out = path.join(__dirname, "auth-server-technical-design.docx");
  fs.writeFileSync(out, buf);
  console.log("wrote", out, buf.length, "bytes");
});
