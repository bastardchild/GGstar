# GGstar

**GitHub reputation as a soulbound badge on BOT Chain.**

GGstar analyzes your GitHub profile with AI, lets you curate the result,
then mints it as a **non-transferable ERC-721 (Soulbound Token)** on **BOT Chain Testnet (968)**.
Anyone can verify the result on-chain, and holders can embed a badge in their README.

Built for **Build Week Hackathon Vol.2 - Track AI & RWA**.

🌐 **Live: https://ggstar.sbs** (contracts on BOT Chain Testnet 968 and Mainnet 677 - see Deployment).

---

## How to use

1. Open https://ggstar.sbs and click **Sign in with GitHub**.
2. Click **Analyze my skill** - your own GitHub profile is analyzed, no username to type.
3. Curate the result: pick an avatar, choose a title, remove skills that do not fit.
4. Click **Mint badge on BOT Chain** and confirm in MetaMask (BOT Chain Testnet, chain ID 968).
5. Share: copy the Markdown snippet into a GitHub README, or post to X tagging `@BOTChain_ai`.
6. Anyone can verify the badge on the explorer or at `/api/badge/<wallet>.svg` - no login needed.

---

## What it does

1. **Sign in with GitHub** - OAuth (`read:user` only). The login comes from GitHub's
   `/user` endpoint, never from the browser.
2. **Analyze** - one click fetches **your own** GitHub profile, repos, languages,
   organisations and commit events, then computes a skill score, top skills,
   suggested titles and a summary. The username always comes from your GitHub
   login session; there is no username input to change.
3. **Curate** - pick 1 of 100 unique avatars, choose a title (AI suggestion or custom),
   and **remove AI skills that do not fit**. Only the final list goes on-chain.
4. **Mint** - MetaMask signs `mintBadge(...)`. Metadata is generated **server-side** into a
   `data:application/json;base64` URI, so the frontend cannot forge a badge.
   Minting is only allowed for **your own** GitHub account.
5. **Verify** - the server reads the mint transaction back from the chain, matches the
   `BadgeMinted` event against your OAuth login, and records the result. Badges whose owner
   never proved the username are shown as **⚠ Unverified claim**, not silently trusted.
6. **Share** - link straight to the explorer, one-click share to X tagging
   `@BOTChain_ai`, and copy HTML/Markdown for a GitHub README.
7. **Explore** - leaderboard by total stars (**registered users only**), plus a 40+
   achievement grid derived from GitHub data.

---

## Architecture

```
Browser (Alpine.js + Ethers.js + Tailwind; shared CSS/JS in public/, pages in views/partials)
        │  fetch /api/analyze │ /api/token-uri │ /api/claim │ /api/badge/:addr.svg
        ▼
Go Fiber backend ── SQLite (ggstar.db, WAL)  ← profiles, users, claims, sessions
        ├── sessions (SQLite, not Redis: an LRU policy must never drop a login)
        ├── cache: in-process or Redis 8 (snapshots 10 min, rate-limit counters)
        ├── GitHub REST API + GitHub OAuth (read:user)
        ├── OpenAI-compatible Chat Completions (+ mock fallback)
        └── read-only JSON-RPC → BOT Chain (badge reads + event verification)
        ▼
ggstarSkillSBT.sol  (ERC-721 + ERC721URIStorage, OZ v5)
```

| Layer | Choice | Why |
|---|---|---|
| Backend | Go 1.25 + Fiber v2 | single static binary, fast, tiny image |
| Database | `modernc.org/sqlite` (pure Go) | `CGO_ENABLED=0` → Alpine image stays small |
| Sessions | custom SQLite `fiber.Storage` | Fiber's official sqlite3 store needs cgo |
| Cache | in-process (default) or **Redis 8** | opt-in; survives restarts, shared across replicas |
| Contract | OZ v5 `_update` override | v5 hook for soulbound enforcement |
| Metadata | server-generated data URI | tamper-proof without an IPFS dependency |
| Avatars | DiceBear **Sprouts** (CC0 1.0) via `dicebear-go/v10` | 100 unique, deterministic SVG avatars |
| Deploy | multi-stage Docker (53 MB) | one command, works anywhere |

---

## Avatars

The 100 selectable avatars are **not** hand-drawn files checked into the repo. They are
generated at build time by `tools/genavatars`, which uses the official DiceBear Go library:

| | |
|---|---|
| Style | **Sprouts** (potted plants with faces) |
| License | **CC0 1.0** - public domain, no attribution required, commercial use allowed |
| Library | `github.com/dicebear/dicebear-go/v10` + `github.com/dicebear/styles/v10` (pure Go, no cgo) |
| Output | `public/avatars/001.svg` … `100.svg` (≈4 KB each) |
| Determinism | same seed → byte-identical SVG, so regenerating never produces a diff |

**Uniqueness is enforced, not hoped for.** The generator fails the build (`exit 1`) if any of
these break:

- fewer or more than exactly 100 avatars are produced
- two avatars render identical SVG
- two avatars define the same internal SVG id (this is what makes it safe to inline an
  avatar into the README badge)
- stale files from an older generator are left in the output directory

Verified across the shipped set: **100/100 distinct**, drawing on 12 plant variants × 7 pot
variants × 11 eye styles × 11 mouth styles × 8 pastel backgrounds.

Avatars are deterministic because the generator pins two options that would otherwise vary:
`idRandomization: false` (keeps ids seed-derived and stable) and `animationVariant: "none"`
(Sprouts is an animated style; a static badge must not carry CSS animation classes).

```bash
# regenerate locally (writes into public/avatars)
docker run --rm -v "$PWD:/src" -w /src/tools/genavatars golang:1.25-alpine go run . -out /src/public/avatars
```

In Docker the generation happens inside the builder stage, and `public/avatars/` is
`.dockerignore`d so a stale local copy can never leak into the image.

> **Not exclusive:** the contract stores only the numeric `avatarId` (1–100), so two wallets
> may still pick the same avatar. Making it exclusive would need a new mapping and a
> redeploy. Deriving the avatar from the wallet address instead is the cheaper path if that
> becomes a requirement.

---

## Quick start (Docker only)

No local Go, Node or Python needed - the toolchain runs in containers.

```bash
cp .env.example .env      # then fill AI_API_KEY / GITHUB_TOKEN / OAuth / CONTRACT_ADDRESS
docker compose up -d --build
curl http://localhost:3000/healthz
```

Open <http://localhost:3000>.

---

## GitHub OAuth setup

Sign-in is required to claim a profile, so an OAuth App is needed for local runs too.

1. Go to <https://github.com/settings/developers> → **New OAuth App**.
2. Set **Authorization callback URL** to exactly:

   ```
   http://localhost:3000/auth/github/callback
   ```
   (in production, `https://ggstar.sbs/auth/github/callback` - it must match
   `PUBLIC_BASE_URL` + `/auth/github/callback` character for character, or GitHub returns
   `redirect_uri_mismatch`.)
3. Copy the Client ID into `GITHUB_OAUTH_CLIENT_ID` and generate a client secret for
   `GITHUB_OAUTH_CLIENT_SECRET`.
4. **Behind HTTPS set `COOKIE_SECURE=true`.** With the value left at `false` on an HTTPS
   host the session cookie is not stored and login fails silently.
5. Restart: `docker compose up -d --force-recreate ggstar`, then confirm `/healthz`
   reports `"oauthEnabled": true`.

Only the `read:user` scope is requested; GGstar never asks for write access.

All other commands are equally containerized:

```bash
# run the test suite
docker run --rm -v "$PWD:/src" -w /src golang:1.25-alpine go test ./...

# compile the contract + regenerate the ABI
docker run --rm -v "$PWD:/w" -w /w node:22-alpine \
  sh -c "npm i solc@0.8.26 @openzeppelin/contracts@5.0.2 && node tools/compile.js"
```

---

## Deployment

**BOT Chain Testnet (Chain ID 968)**

| | |
|---|---|
| Contract address | `0x00D3f8529785daBB3C45d6cfdC7837d7812559Bd` |
| Deploy tx | `0xa719a0c834131a28f230508ca41a3993291f4068b5a3ebc817b9a5fe06002cab` |
| Explorer | `https://scan.bohr.life/address/0x00D3f8529785daBB3C45d6cfdC7837d7812559Bd` |
| RPC | `https://rpc.bohr.life` |

**BOT Chain Mainnet (Chain ID 677)**

| | |
|---|---|
| Contract address | `0x00D3f8529785daBB3C45d6cfdC7837d7812559Bd` |
| Deploy tx | `0x7e775b79f5b8af980d623725fed658f0dd7b9fbc1827dafb5c363280ae3dcd33` |
| Explorer | `https://scan.botchain.ai/address/0x00D3f8529785daBB3C45d6cfdC7837d7812559Bd` |
| RPC | `https://rpc.botchain.ai` |

Contract: `contracts/ggstarSkillSBT.sol` ("ggstar Skill Badge", symbol `KAWAII`),
Solc **0.8.26**, optimizer enabled / **200 runs**, OpenZeppelin **5.0.2**.

**Production:** https://ggstar.sbs (`PUBLIC_BASE_URL=https://ggstar.sbs`,
`COOKIE_SECURE=true`, `CACHE_DRIVER=redis`, OAuth callback
`https://ggstar.sbs/auth/github/callback`).

**For judges:** the mainnet contract page is
`https://scan.botchain.ai/address/0x00D3f8529785daBB3C45d6cfdC7837d7812559Bd?tab=txs`
(same link as the footer "BOT Chain Explorer" button on the live site).

The app reads both networks: badge lookups try mainnet first, then fall back to testnet, so
the same wallet works on either chain.

---

## Deploying the contract (Remix)

1. Open <https://remix.ethereum.org>, create `ggstarSkillSBT.sol` and paste
   `contracts/ggstarSkillSBT.flat.sol` from this repo (flattened: OpenZeppelin 5.0.2
   already inlined, no imports to resolve).
2. Compile with **Solidity 0.8.26**, optimizer enabled / 200 runs, EVM version
   **`paris`** (never shanghai/cancun - PUSH0 risk on BOT Chain).
3. In MetaMask add **BOT Chain Testnet**:

   | Field | Value |
   |---|---|
   | Network name | BOT Chain Testnet |
   | RPC URL | `https://rpc.bohr.life` |
   | Chain ID | `968` (`0x3c8`) |
   | Symbol | `BOT` |
   | Explorer | `https://scan.bohr.life` |

4. Get test gas at <https://faucet.botchain.ai> (10 tBOT / 24 h).
5. Deploy, then verify the source on <https://scan.bohr.life>.
6. Put the address into `.env`:

   ```env
   CONTRACT_ADDRESS=0x00D3f8529785daBB3C45d6cfdC7837d7812559Bd
   CONTRACT_ADDRESS_MAINNET=0x00D3f8529785daBB3C45d6cfdC7837d7812559Bd
   ```

7. Update `config/contract.json` (`contractAddress`, `deployTxHash`, `deployed: true`).
   The ABI is already in that file - the frontend loads it from `/api/config`.
8. Deploy the same contract to **BOT Chain Mainnet (677)** (done - same address as
   testnet, deploy tx `0x7e775b79f5b8af980d623725fed658f0dd7b9fbc1827dafb5c363280ae3dcd33`).
   The hackathon requires both; the app reads mainnet first, then testnet.

---

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `3000` | |
| `DB_PATH` | `/data/ggstar.db` | inside the container volume |
| `CACHE_DRIVER` | `memory` | `memory` or `redis`. Falls back to memory if Redis is unreachable |
| `CACHE_KEY_PREFIX` | `ggstar:` | namespace for Redis keys |
| `REDIS_URL` | `redis://redis:6379/0` | used when `CACHE_DRIVER=redis` |
| `REDIS_PASSWORD` | *(empty)* | **set this in production**; Redis 8 has no auth by default |
| `REDIS_DB` | `0` | |
| `AI_BASE_URL` | `https://api.openai.com/v1` | OpenAI, Gemini, DeepSeek, Groq, Ollama, vLLM |
| `AI_API_KEY` | *(empty)* | empty → deterministic **mock fallback**, demo still works |
| `AI_MODEL` | `gpt-4o-mini` | |
| `GITHUB_TOKEN` | *(empty)* | **strongly recommended**: 60 → 5000 req/h |
| `GITHUB_OAUTH_CLIENT_ID` | *(empty)* | from your GitHub OAuth App |
| `GITHUB_OAUTH_CLIENT_SECRET` | *(empty)* | server-side only, never sent to the browser |
| `SESSION_TTL_HOURS` | `72` | session lifetime |
| `PUBLIC_BASE_URL` | `http://localhost:3000` | must match the OAuth callback host exactly |
| `COOKIE_SECURE` | `false` | **set `true` behind HTTPS**, or login silently fails |
| `GITHUB_API_URL` | `https://api.github.com` | |
| `NETWORK_CHAIN_ID` / `NETWORK_CHAIN_ID_HEX` | `968` / `0x3c8` | |
| `RPC_URL` | `https://rpc.bohr.life` | |
| `EXPLORER_URL` | `https://scan.bohr.life` | testnet explorer |
| `FAUCET_URL` | `https://faucet.botchain.ai` | |
| `NATIVE_SYMBOL` | `BOT` | |
| `CONTRACT_ADDRESS` | testnet address | deploy output; empty → minting disabled |
| `RATE_LIMIT_PER_MIN` | `20` | per IP; applies to `/api/analyze`, `/api/token-uri`, `/api/claim` |

> **Note:** `scan.botchain.ai` is the **mainnet (677)** explorer.
> The testnet explorer is **`scan.bohr.life`**. Mainnet variables are included in
> `.env.example` for a post-hackathon move.

---

## API

**Public** (no session required - the badge endpoints must stay open or every GitHub
README embed breaks):

| Method | Route | Purpose |
|---|---|---|
| `GET` | `/healthz` | liveness, live RPC chain-id probe, cache driver + reachability |
| `GET` | `/api/config` | runtime config + ABI (keeps ABI out of templates) |
| `GET` | `/api/me` | session identity + CSRF token + sign-in/out URLs |
| `GET` | `/auth/github` | start OAuth (redirects to GitHub) |
| `GET` | `/auth/github/callback` | OAuth callback; validates `state` |
| `GET` | `/api/badge/:address` | badge JSON + README snippet |
| `GET` | `/api/badge/:address.svg` | embeddable SVG (always 200, empty state when none) |
| `GET` | `/public/*` | avatars and vendored assets |

**Requires sign-in:**

| Method | Route | Purpose |
|---|---|---|
| `GET` | `/`, `/leaderboard`, `/achievements` | pages, fully public (no login needed to browse) |
| `POST` | `/api/analyze` | `{refresh?, achievements?}` → analysis, stats, achievements for **your own** login (`username` in body is ignored) |
| `POST` | `/api/token-uri` | server-generated metadata; **403** unless `username` == your login |
| `POST` | `/api/claim` | `{txHash}` → verifies the mint against your identity on-chain |
| `POST` | `/auth/logout` | destroy the session |
| `GET` | `/api/leaderboard` | top 50 by total stars (**registered users only**) |
| `GET` | `/api/achievements/:username` | 40+ achievements with lock state |
| `GET` | `/api/snippet` | `?address=0x…` → HTML/Markdown/tweet |

### Authentication model

Analysis is **self-only**: `POST /api/analyze` always analyzes the signed-in GitHub
login - any username sent in the request body is ignored server-side, and the UI has
no username input to change. What OAuth gates is the **identity claim**:

- The signed-in login comes from GitHub's `/user` endpoint and is stored server-side in
  SQLite. Nothing the browser says about its own identity is trusted.
- `POST /api/token-uri` refuses to build metadata for a username other than your own.
- After minting, `POST /api/claim` reads the transaction back from the chain, decodes the
  `BadgeMinted` event, and compares its `githubUsername` with your OAuth login.

**Known limitation.** Minting happens directly from the browser to the contract
(`mintBadge` through MetaMask), so the server is not in the trust path. A determined user
can bypass the UI and mint someone else's username. That is why verification exists: such a
badge is recorded as **unverified** and its README widget visibly reads
"⚠ Unverified claim". Making the claim cryptographically binding on-chain would require a
server-signed attestation plus `ecrecover` in the contract, which needs a redeploy.

### Cache

`CACHE_DRIVER=redis` stores GitHub snapshots and rate-limit counters in Redis 8, so they
survive restarts and are shared across replicas (`INCR` is atomic server-side). Redis is
never a hard dependency: if the connection fails at startup the app logs the reason and
falls back to the in-process store, and if Redis dies later the app keeps serving (the
limiter fails open and `/healthz` reports `cacheOk: false`).

Sessions deliberately stay in SQLite. Putting them in Redis with an `allkeys-lru` policy
would let eviction silently log users out.

---

## Verification flow (Definition of Done)

1. Sign in with GitHub (OAuth, `read:user`).
2. MetaMask connected to BOT Chain **968** (auto-switch via `wallet_addEthereumChain`).
3. Analyze your own profile → live preview renders.
4. Remove some AI skills, pick an avatar and title.
5. Mint → transaction hash appears.
6. Open the hash on `https://scan.bohr.life/tx/0x…`.
7. The success modal reports **GitHub verified**, because the server matched the
   `BadgeMinted` event against your login.
8. `GET /api/badge/<wallet>.svg` renders the minted badge, publicly, with no login.
9. Copy the Markdown snippet into a GitHub README.
10. Share to X with the `@BOTChain_ai` tag.

Proving the negative is just as important: sign in as A, mint for B (via the console), and
the badge widget renders **⚠ Unverified claim** - the badge is real on-chain, but it never
claims an identity it cannot prove.

---

## Security notes

- **Soulbound enforced on-chain** in `_update`; transfers revert with `Soulbound()`.
- **Metadata never trusts the client**: `tokenURI` is generated server-side from the
  curated payload, and the contract stores the badge fields itself in `badgeDetails`.
- **Identity comes from OAuth, not from the browser.** `POST /api/token-uri` returns 403
  unless the requested username matches the session login (case-insensitive).
- **OAuth `state` is verified and single-use**, stored in the session and cleared on
  consumption, which blocks login-CSRF and replay.
- **Session fixation is prevented** with `Regenerate()` on sign-in.
- **CSRF is enforced** on every state-changing request through `X-Csrf-Token`; sessions use
  `HttpOnly`, `Secure` (in production) and `SameSite=Lax` cookies.
- **Rate limiting** on the endpoints that cost GitHub quota or AI tokens; fails open if the
  limiter backend is down rather than blocking traffic.
- **Username validation** (`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`) blocks
  path traversal and SSRF through the GitHub base URL.
- **Score clamped 1..100** in the AI layer *and* re-checked on-chain (`InvalidSkillScore`).
- **SVG output is XML-escaped** and length-capped, so profile text cannot inject markup.
- **Only `read:user` is requested** from GitHub - the token cannot write to your account.
- **`.env` is never baked into the image** (`.dockerignore`) and is git-ignored.
- **The `/api/badge/*` surface is intentionally public** and covered by a regression test,
  because gating it would break every README widget.

---

## Project layout

```
ggstar/
├── main.go                       routes, middleware, wiring
├── integration_test.go           auth-gated flow, split-brain gate, CSRF, public routes
├── contracts/ggstarSkillSBT.sol  soulbound ERC-721
├── config/contract.json          address + ABI + network metadata
├── internal/
│   ├── achievements/  40+ achievements + ladders
│   ├── ai/            OpenAI-compatible client + mock fallback
│   ├── auth/          GitHub OAuth + session helpers + RequireAuth
│   ├── avatar/        avatar id → file lookup + inline markup
│   ├── badgesvg/      README embeddable SVG + snippets
│   ├── cache/         Store interface: in-process + Redis 8 drivers
│   ├── chain/         JSON-RPC client, ABI decoder, receipt/log verification
│   ├── config/        env loading
│   ├── contract/      contract.json loader
│   ├── db/            SQLite schema, migrations, queries
│   ├── github/        GitHub REST client + snapshot builder
│   ├── model/         shared types
│   ├── service/       orchestration + tokenURI + claim verification
│   └── sessionstore/  fiber.Storage on modernc sqlite (avoids cgo)
├── views/             index / leaderboard / achievements + partials/ (head, nav, footer, icons)
├── public/
│   ├── app.css / app.js / achievements.js   shared styles + frontend logic
│   ├── vendor/        Alpine.js, ethers.js (MIT, vendored)
│   └── avatars/       001.svg … 100.svg (generated at build time, not committed)
└── tools/
    ├── genavatars/    DiceBear Sprouts generator (own Go module)
    └── compile.js     recompiles the contract and refreshes the ABI
```

---

## Testing

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.25-alpine go test ./...
docker run --rm -v "$PWD:/src" -w /src golang:1.25-alpine go vet ./...
```

Covered:

| Package | What is asserted |
|---|---|
| `main` (integration) | public routes stay public, badge SVG stays public, anonymous gets 401/302, **analyze is self-only (body username ignored)**, minting another username is 403, own profile is allowed, case-insensitive login match, CSRF blocks unsigned POSTs, `/api/me` exposes identity + token |
| `auth` | authorize URL contains state/scope/redirect and leaks no secret, only `read:user` is requested, state is random/single-use/rejects mismatches, sign-in/out lifecycle, `RequireAuth` behaviour |
| `chain` | struct ABI decode (both layouts), receipt fetch (pending vs mined), `BadgeMinted` log decode, foreign-contract and wrong-topic rejection, keccak selectors vs known values |
| `sessionstore` | round-trip, missing key returns `nil` (not an error), expiry, update-in-place, delete/reset, zero-expiration semantics, shared-pool close |
| `cache` | in-process TTL semantics, `Incr` window reset, struct round-trip, driver selection, **graceful fallback when Redis is unreachable** |
| `avatar` | all 100 exist and are well-formed XML, all 100 distinct, no colliding SVG ids, deterministic inline, id clamping |
| `badgesvg` | well-formed XML, XSS escaping, score clamping, snippet/tweet links, avatar inlined (not externally referenced), **verified vs unverified rendering** |
| `ai` | markdown-fence stripping, score clamping, skill dedupe, deterministic mock, provider failure → mock |
| `db` | round-trip, star TTL preservation, **leaderboard excludes searched profiles**, ownership survives an anonymous lookup, registered-user requirement, user upsert/rename, claim round-trip |
| `achievements` | ladder thresholds, UTC schedules, locked/unlocked catalogue |
| `service` | username validation, base64 metadata validity, `image` follows `avatarId`, ownership decision matrix |

---

## Licenses

- **Code:** MIT.
- **Avatars:** [Sprouts](https://www.dicebear.com/styles/sprouts/) by
  [DiceBear](https://www.dicebear.com), licensed **CC0 1.0** (public domain).
  Generated via the official [`dicebear-go`](https://github.com/dicebear/dicebear-go) library.
- **Vendored frontend assets** (`public/vendor/`): Alpine.js (MIT), ethers.js (MIT).
- **Contract:** OpenZeppelin Contracts v5 (MIT).
