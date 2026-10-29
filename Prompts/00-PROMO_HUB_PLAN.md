# PROMO_HUB_PLAN.md — Promo monitoring + cross-posting přes Apify × IFTTT

Handoff pro Claude Code (Opus). Jazyk implementace: **Go** (žádný Python, žádný Bash nad rámec jednořádkových příkazů v Makefile/compose). Jazyk dokumentace a commitů: čeština nebo angličtina, identifikátory v kódu anglicky.

## 0. TL;DR

- Jeden Go service **`promohub`** na JODA (Docker Compose, Postgres, Caddy, Cloudflare Tunnel → `promo.ol1n.com`).
- **Apify** = sběr dat tam, kde není levné/free oficiální API (X, TikTok, Instagram, Reddit vyhledávání zmínek). Actors ze Store běží v cloudu Apify — my žádný scraper nepíšeme.
- **IFTTT.ai** = spouštěče (Webhooks, YouTube/Bluesky/Instagram "new post by you"), AI Agent (hlídání stránek, AI Action assist pro texty notifikací) a levné akce (post na X/Bluesky, Telegram, Discord, Sheets).
- **Oficiální free API** všude, kde jde o *vlastní* účty: YouTube Data/Analytics, Bluesky AT Protocol, Reddit OAuth, App Store Connect, Google Play Developer. Apify se tím šetří na minimum.
- Obě varianty toku: **A) Apify → IFTTT** (data → notifikace/akce) a **B) IFTTT → Apify** (událost → hloubkový scrape → akce).
- Human-in-the-loop: každý post schvaluje Ol1n v Telegramu. Žádné autonomní publikování.
- Navazuje na `DISCORD_BOT_PLAN.md` (`apps.yaml` jako source of truth) a nahrazuje posting/monitoring část `OPENCLAW_PROMO_PLAN.md`.

## 1. Cíle

1. **Monitoring vlastních promo účtů**: X, Bluesky, Reddit, TikTok, YouTube, Instagram, App Store, Google Play.
   - Metriky: likes, views/impressions, reposty/retweety/shares, komentáře/replies, followers, recenze + hvězdičky, downloads (kde API dovolí).
   - Události: nový komentář, nová recenze, nová zmínka (mention / keyword), skok v metrice (threshold), nový follower milestone.
2. **Cross-posting**: z jednoho zdrojového obsahu (nové YouTube video, nový Bluesky post, nová verze appky) vygenerovat varianty pro X, Bluesky, Reddit, Instagram (+ TikTok/YT Shorts později), nechat schválit v Telegramu, publikovat.
3. **Denní/týdenní digest** do Telegramu a Discordu (přes existující bot / IFTTT Discord akci).
4. Vše řízeno z jednoho `apps.yaml` + `accounts.yaml`.

## 2. Non-goals (v1)

- Vlastní scrapery. Používáme Apify Store actors nebo oficiální API.
- Automatické publikování bez schválení.
- TikTok a YouTube **posting** (TikTok Content Posting API vyžaduje audit aplikace; YouTube upload přes IFTTT/YouTube Data API až ve fázi 5).
- Instagram **posting** přes IFTTT — IFTTT má pro Instagram jen triggery (nový post by you), ne akci "publish". Řeší se přes Meta Graph API ve fázi 4.
- Twitch, Threads, LinkedIn.

## 3. Rozhodnutí, která už padla

| Téma | Rozhodnutí |
|---|---|
| Jazyk | Go 1.23+, stdlib + `github.com/apify/apify-client-go` (oficiální, experimental, čistě stdlib) |
| Storage | Postgres na JODA, `sqlc` + `golang-migrate` (stejně jako ostatní služby) |
| Deploy | Docker Compose na JODA, Caddy reverse proxy, Cloudflare Tunnel `promo.ol1n.com` |
| LLM pro varianty postů | primárně **Spark** přes LiteLLM (`/v1/chat/completions`), fallback IFTTT AI Action assist pro krátké notifikační texty |
| Schvalování | Telegram bot (inline tlačítka Approve / Edit / Skip) — vlastní Go handler, ne IFTTT |
| Notifikace | Telegram (primár), Discord (přes IFTTT Discord akci nebo existující Ol1nBot webhook) |
| Dedup/diff | vždy na straně `promohub` (DB), ne v IFTTT filter code |
| Apify plán | začít na Free ($5 kreditů/měsíc), přejít na Starter podle spotřeby |
| IFTTT plán | Starter ($2,99) nebo Pro — potřebujeme polling 5 min, Webhooks, Bluesky/X akce, AI Agent kredity |

## 4. Mapa platforem — odkud brát co

Legenda: 🆓 oficiální API zdarma, 🅰 Apify actor, 🔁 IFTTT služba (trigger/akce), ✍️ posting.

| Platforma | Vlastní metriky (likes/views/comments/followers) | Zmínky / keyword search | Posting | Poznámky |
|---|---|---|---|---|
| **X** | 🅰 Tweet/profile scraper (public like/view/repost/reply counts) | 🅰 search actor | 🔁 IFTTT X "Post a tweet" | X API free tier = 100 reads/měsíc → nepoužitelné na čtení, Apify je zlomek ceny. Impressions = veřejný "views" count na tweetu. |
| **Bluesky** | 🆓 AT Protocol `app.bsky.feed.getAuthorFeed`, `getPostThread` (likeCount, repostCount, replyCount, quoteCount) bez auth | 🆓 `app.bsky.feed.searchPosts` | 🔁 IFTTT Bluesky "Create text/link/image post" nebo 🆓 přímo `com.atproto.repo.createRecord` z Go | Bluesky nemá impressions. IFTTT má i trigger "New post by author" a "New post from search". |
| **Reddit** | 🆓 OAuth script app: `/user/<me>/submitted`, `/comments/<id>` (score, num_comments); post insights (views) jen pro vlastní posty přes `/api/info` + `view_count` není garantované | 🅰 Reddit scraper (keyword / subreddit new) nebo 🆓 `search.json` (rate-limited, bez auth 10 rpm) | 🔁 IFTTT Reddit "Submit a link/text post" nebo 🆓 API z Go | Reddit API free pro osobní použití, OAuth povinné. |
| **TikTok** | 🅰 TikTok profile/video scraper (plays, diggCount, shareCount, commentCount) | 🅰 TikTok hashtag/search | ❌ v1 | Posting přes TikTok Content Posting API = audit → fáze 5+. |
| **YouTube** | 🆓 YouTube Data API v3 `videos.list?part=statistics` (views, likes, comments) + `commentThreads.list`; 🆓 YouTube Analytics API (impressions, CTR, watch time) pro vlastní kanál | 🅰 YouTube search actor (zmínky ve videích/komentářích) | 🔁 IFTTT YouTube trigger "New video by you" (zdroj pro cross-post); upload až fáze 5 | Data API quota 10 000 units/den zdarma, bohatě stačí. |
| **Instagram** | 🆓 Meta Graph API (Business/Creator účet + FB Page): `/{ig-user-id}/media?fields=like_count,comments_count` + `/insights` (impressions, reach, saves) | 🅰 Instagram hashtag/mentions scraper | 🆓 Graph API Content Publishing (image/video/reels) z Go | 🔁 IFTTT Instagram = jen triggery "New photo/video by you" (business účet). Bez business účtu → 🅰 profile scraper (jen veřejné likes/comments). |
| **App Store** | 🆓 App Store Connect API `customerReviews` + Sales/Analytics reports (downloads, impressions, page views); 🆓 veřejný RSS `itunes.apple.com/rss/customerreviews/id=<id>/sortBy=mostRecent/json` | — | — | RSS je nejjednodušší pro recenze (bez auth), ASC API pro downloads. |
| **Google Play** | 🆓 Google Play Developer API `reviews.list` (posledních 7 dní), Reporting API (ratings, crashes); statistiky stahování přes GCS export | — | — | 🅰 Play reviews scraper ($0,08/1k) jen jako fallback. |

**Důsledek:** Apify je v v1 nutné jen pro **X, TikTok a keyword-monitoring (Reddit/YouTube/Instagram zmínky)**. Zbytek je zdarma přes oficiální API, což drží měsíční náklady pod ~10 $.

## 5. Architektura

```
                ┌──────────────────────────────────────────────────────────┐
                │                      IFTTT.ai (cloud)                     │
                │  Triggers: Webhooks, YouTube new video, Bluesky new post, │
                │            Instagram new post, AI Agent "New item on page"│
                │  Actions:  X post, Bluesky post, Reddit post, Telegram,   │
                │            Discord, Sheets, Apify Run Task, Webhooks      │
                │  AI:       AI Agent action, AI Action assist              │
                └───────▲──────────────────────────────┬───────────────────┘
                        │ Webhooks (JSON)              │ POST /ifttt/event
                        │                              ▼
┌───────────────┐   ┌───┴──────────────────────────────────────────────┐   ┌──────────────┐
│  Apify (cloud)│──▶│  promohub (Go, JODA, promo.ol1n.com)              │◀──│ Telegram bot │
│  Store actors │   │  - /webhooks/apify   (ACTOR.RUN.SUCCEEDED)        │   │ (approvals)  │
│  schedules    │◀──│  - /webhooks/ifttt   (IFTTT → hub)                │   └──────────────┘
│  datasets     │   │  - collectors (cron): YT, Bsky, Reddit, ASC, Play │
└───────────────┘   │  - differ + rules engine → events                  │
                    │  - composer (LLM) → post drafts                    │
                    │  - publisher → IFTTT Webhooks / direct APIs        │
                    │  - Postgres (sqlc)                                 │
                    └───────────────┬────────────────────────────────────┘
                                    │ LiteLLM /v1/chat/completions
                                    ▼
                            ┌──────────────┐
                            │ DGX Spark    │
                            │ vLLM+LiteLLM │
                            └──────────────┘
```

### 5.1 Varianta A — Apify → IFTTT (data tečou ven)

1. **Apify Schedule** spouští Task (např. `x-profile-task`, `tiktok-profile-task`, `reddit-mentions-task`) každých N hodin.
2. Apify **webhook** `ACTOR.RUN.SUCCEEDED` → `POST https://promo.ol1n.com/webhooks/apify` (payload obsahuje `resource.defaultDatasetId`). Rychlejší a spolehlivější než IFTTT trigger "Task run finished" (ten je polling 5 min / 1 h).
3. `promohub` stáhne dataset přes `apify-client-go`, uloží snapshot, spočítá **delta** proti předchozímu (nové komentáře, nové zmínky, změny metrik).
4. Rules engine vyhodnotí pravidla z `rules.yaml` (viz §7) a vytvoří `events`.
5. Každý event odejde na **IFTTT Webhooks** (`https://maker.ifttt.com/trigger/{event}/json/with/key/{key}`) s JSON payloadem. IFTTT applety pak:
   - `promo_mention` → Telegram zpráva (AI Action assist napíše shrnutí a doporučenou reakci),
   - `promo_review` → Telegram + řádek do Google Sheets,
   - `promo_metric_jump` → Discord kanál,
   - `promo_digest` → e-mail/Telegram.
6. Pro "rychlý pohled" bez vlastního kódu lze mít i čistě IFTTT applet: *If* Apify "Task run finished" → *Query* "Get dataset items" → *AI Agent action* "najdi negativní komentáře a pošli je na Telegram". Hodí se na prototypování pravidla, než se přepíše do Go.

### 5.2 Varianta B — IFTTT → Apify (událost spouští sběr)

1. **IFTTT trigger** (YouTube "New video by you", Bluesky "New post by author", Instagram "New photo by you", App Store RSS "New feed item", Telegram "New message with key phrase `/scan`", AI Agent "New item on a page" pro lehké veřejné stránky jako HN search / Product Hunt / subreddit `new`).
2. Akce buď **Apify "Run Task"** s input overrides (`{"startUrls":[{"url":"{{Url}}"}]}`) — výsledek pak dojede do hubu cestou varianty A — nebo **Webhooks → `POST /webhooks/ifttt`** a hub si Task spustí sám přes Go klienta (preferované: hub má kontext a dedup).
3. Typické řetězce:
   - Nové YouTube video → hub založí `content_item` → composer vygeneruje varianty → Telegram schválení → publikace (§5.3).
   - Nový příspěvek na Bluesky/X od mě → za 24 h a 72 h spustit scrape toho konkrétního postu (engagement follow-up), uložit metriky.
   - AI Agent hlídá `news.ycombinator.com/newest` (nebo `reddit.com/r/<sub>/new`) na klíčové slovo → Run Actor stáhne celé vlákno → hub pošle do Telegramu s návrhem odpovědi.
   - `/scan <url>` v Telegramu → IFTTT Telegram trigger → hub → Apify "Scrape Single URL" → AI shrnutí zpět.

### 5.3 Cross-posting pipeline

```
source (YT video | Bluesky post | app release z apps.yaml | ruční /post v Telegramu)
  → composer: 1 prompt → JSON {x, bluesky, reddit{sub,title,body}, instagram{caption,hashtags}, tiktok_caption}
  → drafts uloženy (status=draft)
  → Telegram karta s tlačítky per platforma: ✅ Approve / ✏️ Edit / ⏭ Skip / 🕒 Schedule
  → publisher:
      X        → IFTTT Webhooks event `publish_x` → IFTTT akce "Post a tweet" (s obrázkem přes "Post a tweet with image")
      Bluesky  → přímo AT Protocol z Go (obrázky, link card) — IFTTT jen fallback
      Reddit   → přímo Reddit API z Go (potřeba zvolit subreddit a respektovat self-promo pravidla)
      Instagram→ Meta Graph API (fáze 4), vyžaduje obrázek/video na veřejné URL (hub ho vystaví pod promo.ol1n.com/media/…)
      TikTok   → fáze 5 (zatím jen připravit caption do schránky / Telegramu)
  → po publikaci uložit `post_id`/URL → zařadit do monitoringu (follow-up scrape 24 h / 72 h / 7 d)
```

Composer bere styl z `voice.md` (tón, zakázané fráze, hashtag sady per appka) a z `apps.yaml` (name, store links, one-liner, screenshots).

## 6. Repo struktura

```
promohub/
├── cmd/promohub/main.go            # HTTP server + cron scheduler v jednom procesu
├── internal/
│   ├── config/                     # apps.yaml, accounts.yaml, rules.yaml, voice.md loader
│   ├── store/                      # sqlc generated, migrations/
│   ├── apify/                      # wrapper nad apify-client-go: RunTask, GetDataset, webhook parsing + HMAC check
│   ├── ifttt/                      # Webhooks client (trigger event), inbound /webhooks/ifttt handler
│   ├── collectors/                 # jeden package per zdroj, společné rozhraní
│   │   ├── youtube/   (Data API + Analytics API)
│   │   ├── bluesky/   (AT Protocol, bez SDK, stdlib)
│   │   ├── reddit/    (OAuth script app)
│   │   ├── appstore/  (RSS reviews + App Store Connect JWT)
│   │   ├── googleplay/(Play Developer API, service account)
│   │   ├── instagram/ (Graph API)
│   │   └── apifysrc/  (X, TikTok, mentions — mapování datasetů na společný model)
│   ├── differ/                     # snapshot diff → deltas
│   ├── rules/                      # rules.yaml → events
│   ├── composer/                   # LLM přes LiteLLM (OpenAI-compatible), prompt templates
│   ├── publisher/                  # per-platform publish, retry, idempotence
│   ├── telegram/                   # bot (long polling), approval UI, /post, /scan, /stats
│   └── digest/                     # denní/týdenní souhrn
├── deploy/
│   ├── docker-compose.yml          # promohub + postgres (nebo napojení na sdílený postgres na JODA)
│   └── Caddyfile.snippet           # promo.ol1n.com → promohub:8080
├── apps.yaml  accounts.yaml  rules.yaml  voice.md
└── Makefile                        # build, migrate, sqlc, deploy (ssh joda docker compose up -d)
```

Společné rozhraní collectoru:

```go
type Collector interface {
    Name() string
    Collect(ctx context.Context, acct Account) ([]Snapshot, error) // metriky + nové položky
}
```

## 7. Datový model (Postgres)

- `accounts(id, platform, handle, external_id, auth_ref)` — z `accounts.yaml`
- `apps(id, slug, name, appstore_id, play_package, links jsonb)` — z `apps.yaml`
- `content_items(id, app_id, source_platform, external_id, url, title, body, media jsonb, published_at)` — moje posty/videa/releasy
- `metric_snapshots(id, content_item_id | account_id, platform, taken_at, likes, views, reposts, comments, followers, extra jsonb)`
- `inbound_items(id, platform, kind{comment,review,mention,reply}, external_id UNIQUE, content_item_id?, author, text, rating?, url, created_at, sentiment?, handled bool)`
- `events(id, type, payload jsonb, created_at, sent_to_ifttt_at)`
- `drafts(id, content_item_id, platform, text, media jsonb, status{draft,approved,scheduled,published,skipped}, scheduled_at, published_post_id, published_url)`
- `apify_runs(run_id, task_id, dataset_id, status, started_at, finished_at, items int, cost_usd)` — pro kontrolu útraty

`rules.yaml` příklad:

```yaml
rules:
  - name: new_review
    on: inbound.review
    then: ifttt:promo_review
  - name: negative_review
    on: inbound.review
    when: rating <= 2
    then: [ifttt:promo_review_urgent, telegram:urgent]
  - name: mention
    on: inbound.mention
    when: text matches /(SwypeKids|Hajánek|OrbitronTactics|olin\.now)/i
    then: ifttt:promo_mention
  - name: viral
    on: metric.delta
    when: views_delta_24h > 1000 or likes_delta_24h > 100
    then: [ifttt:promo_metric_jump, discord:milestones]
  - name: follower_milestone
    on: metric.snapshot
    when: followers crosses [100, 500, 1000, 5000]
    then: discord:milestones
```

## 8. IFTTT applety k založení (v1)

| # | If this | Then that | Poznámka |
|---|---|---|---|
| 1 | Webhooks `promo_mention` (JSON) | Telegram message + **AI Action assist**: "Shrň zmínku do 2 vět, odhadni sentiment, navrhni odpověď" | hub posílá `{platform,url,author,text,app}` |
| 2 | Webhooks `promo_review` | Telegram + Google Sheets append | |
| 3 | Webhooks `promo_review_urgent` | Telegram + IFTTT Notification (push) | |
| 4 | Webhooks `promo_metric_jump` | Discord webhook message | |
| 5 | Webhooks `promo_digest` | Email / Telegram | |
| 6 | Webhooks `publish_x` | X "Post a tweet (with image)" | **jediný** kanál publikace na X v v1 |
| 7 | YouTube "New video by you" | Webhooks → `POST promo.ol1n.com/webhooks/ifttt` | zdroj cross-postu |
| 8 | Bluesky "New post by author" (já) | Webhooks → hub | follow-up monitoring |
| 9 | Instagram "New photo/video by you" | Webhooks → hub | vyžaduje business účet |
| 10 | RSS "New feed item" (App Store reviews RSS per appka) | Webhooks → hub | alternativa k vlastnímu RSS polleru; hub stejně dedupuje |
| 11 | Telegram "New message with key phrase `/scan`" | Webhooks → hub | |
| 12 | AI Agent "New item on a page" (HN newest / subreddit new / Product Hunt) | Apify "Run Task" nebo Webhooks → hub | AI Agent kredity — hlídat spotřebu |

Varianta "bez hubu" pro rychlý test (den 1): *If* Apify "Task run finished" → *Query* Apify "Get dataset items" → *AI Agent* "Run actions across your services": "Z položek vyber komentáře mladší než 24 h, negativní nebo s otázkou, pošli každý na Telegram s návrhem odpovědi; jinak přeskoč a napiš proč."

## 9. Apify tasks k založení

| Task | Actor (ze Store, vybrat podle hodnocení/ceny v den implementace) | Vstup | Plán |
|---|---|---|---|
| `x-profile` | X/Twitter profile + tweets scraper (pay-per-result) | handle, `maxItems: 50` | 2× denně |
| `x-mentions` | X search scraper | `@handle OR "SwypeKids" OR …` | 2× denně |
| `tiktok-profile` | TikTok profile scraper | handle | 1× denně |
| `reddit-mentions` | Reddit scraper (search) | keywords, `sort: new`, `maxItems: 100` | 2× denně |
| `yt-mentions` | YouTube search/comments scraper | keywords | 1× denně |
| `ig-profile` (fallback bez business účtu) | Instagram profile scraper | handle | 1× denně |
| `url-detail` | "Scrape Single URL" / Website Content Crawler | url (dynamicky z hubu) | on-demand |

Každý task: webhook `ACTOR.RUN.SUCCEEDED` → `https://promo.ol1n.com/webhooks/apify?secret=…`. Hub ověřuje secret a `run.actId`/`taskId` proti whitelistu.

Rozpočet: při uvedených frekvencích a `maxItems` ≈ 5–20 tisíc položek/měsíc → odhad **5–15 $/měsíc** (pay-per-result actors ~0,1–1 $/1k). Free plán ($5) stačí na start; hub loguje `cost_usd` z run detailu a posílá týdenní útratu do digestu.

## 10. Fáze a akceptační kritéria

### Fáze 0 — Účty a ruční prototyp (½–1 den, bez kódu)
- Apify účet + token; IFTTT účet (Starter/Pro) + Webhooks key; propojit Apify ↔ IFTTT; připojit X, Bluesky, Telegram, Discord, YouTube, Instagram (business), RSS.
- Založit tasky `x-profile`, `x-mentions`, `reddit-mentions` + schedule.
- Applet "bez hubu" (§8 konec) jako smoke test — ověřit, že AI Agent dává rozumné výstupy a kolik kreditů bere.
- ✅ Do Telegramu dorazí první automatická zmínka z X nebo Redditu.

### Fáze 1 — Hub skeleton + Apify webhook + IFTTT Webhooks (1–2 dny)
- `cmd/promohub`, config loader, Postgres migrace, `/healthz`, `/webhooks/apify`, `/webhooks/ifttt`, IFTTT Webhooks client.
- `apifysrc` collector: mapování datasetů X/Reddit/TikTok na `inbound_items` + `metric_snapshots`; dedup na `external_id`.
- Rules engine s `rules.yaml`, event → IFTTT.
- Compose + Caddy + tunnel `promo.ol1n.com`.
- ✅ Apify run → hub → IFTTT → Telegram end-to-end, duplicity 0.

### Fáze 2 — Free collectors (2–3 dny)
- YouTube Data + Analytics (OAuth refresh token uložený v DB), Bluesky (bez auth pro čtení), Reddit OAuth, App Store (RSS + ASC JWT pro downloads), Google Play (service account).
- Scheduler (robustní `time.Ticker` + jitter; žádný externí cron).
- Telegram bot: `/stats <app|platform>`, `/scan <url>`, denní digest.
- ✅ `metric_snapshots` plné za všechny platformy; digest obsahuje delta 24 h/7 d.

### Fáze 3 — Composer + schvalování + publikace X/Bluesky/Reddit (2–3 dny)
- `composer` přes LiteLLM na Sparku (model dle aktuální konfigurace; fallback Claude API klíč v env).
- Prompt šablony + `voice.md`; výstup strict JSON (validace, retry).
- Telegram approval UI, stavový automat `drafts`.
- Publisher: X přes IFTTT `publish_x`; Bluesky a Reddit přímo z Go; idempotence (`published_post_id`).
- Follow-up scrape publikovaných postů (24 h/72 h/7 d) → metriky zpět k `content_item`.
- ✅ Nové YouTube video → 3 schválené varianty publikovány → do 24 h dorazí engagement report.

### Fáze 4 — Instagram (1–2 dny, závisí na business účtu)
- Graph API: insights collector + Content Publishing (image + reels). Hub servíruje média z `promo.ol1n.com/media/<token>.jpg` s expirací.
- ✅ Instagram varianta publikována ze schválení v Telegramu; likes/reach se sbírají.

### Fáze 5 — Později
- TikTok posting (Content Posting API audit), YouTube Shorts upload, Threads/LinkedIn, Discord bot napojení na `events` místo IFTTT, A/B varianty textů s měřením.

## 11. Kde to poběží a náročnost

- **JODA**: `promohub` + Postgres (Docker Compose). Zátěž zanedbatelná (desítky HTTP requestů za hodinu, < 100 MB RAM).
- **Cloudflare Tunnel + Caddy**: `promo.ol1n.com` pro příchozí webhooky z Apify a IFTTT a pro servírování médií pro Instagram.
- **DGX Spark**: jen LLM inference pro composer (pár volání denně). Pokud Spark spí, composer použije fallback API klíč.
- **Apify cloud**: všechny scrapery. Nic z toho neběží lokálně, nulový Python.
- **IFTTT cloud**: applety, AI Agent, posting na X.
- **Mac Mini M2**: nic (jen dev / `make deploy`).

Pracnost s Opusem v Claude Code: **fáze 0–3 ≈ 6–9 pracovních dní** čistého času, reálně 2 týdny kalendářně kvůli OAuth schvalování (Google, Meta, Reddit app) a čekání na business účet Instagramu. Největší riziko není kód, ale **registrace API přístupů** (YouTube OAuth consent screen, Meta app review pro `instagram_content_publish`, App Store Connect klíč, Play service account). Doporučení: ty vyřídit ručně hned ve fázi 0 paralelně s tím, jak Opus staví fázi 1.

Provozní náklady: Apify 5–15 $/měs., IFTTT 3–7 $/měs., ostatní API zdarma → **do ~20 $/měsíc**. Pro srovnání X API Basic je 200 $/měs.

## 12. Bezpečnost a provoz

- Secrets v `.env` na JODA (Apify token, IFTTT key, Telegram token, OAuth refresh tokeny v DB šifrované `AES-GCM` klíčem z env).
- Webhook endpointy: shared secret v query + rate limit; IFTTT Webhooks nemá podpis, proto hub bere z IFTTT jen "spouštěče", nikdy ne instrukce k publikaci.
- Whitelist Apify task ID. Hub nikdy nespustí actor podle URL, která přišla ze scraped obsahu (prompt-injection přes komentáře).
- Composer nikdy nepublikuje bez `approved` stavu; Telegram příkazy jen z whitelisted chat ID.
- Logy strukturované (`log/slog`), metriky `/metrics` (Prometheus text) pro budoucí Grafanu.

## 13. Otevřené otázky pro Ol1na (odpovědět před fází 1)

1. Instagram: existuje business/creator účet napojený na FB Page? (Rozhoduje fázi 4 vs. Apify fallback.)
2. Reddit: do kterých subredditů se smí postovat a jak často (pravidla self-promo)?
3. Sdílený Postgres na JODA, nebo vlastní instance v compose?
4. Model na Sparku pro composer — aktuální default v LiteLLM, nebo dedikovaný?
5. Má Discord bot (`DISCORD_BOT_PLAN.md`) odebírat `events` z hubu přímo (HTTP/NATS), nebo stačí IFTTT Discord akce v v1?
6. Jazyk postů: CZ+EN varianty per platforma, nebo jen EN?

## 14. První prompt pro Opus

> Přečti `PROMO_HUB_PLAN.md`. Začni fází 1: založ repo `promohub` dle §6, Go 1.23, sqlc + golang-migrate + Postgres, `apify-client-go`. Implementuj `/webhooks/apify` (ověření secretu, stažení datasetu, uložení `inbound_items`/`metric_snapshots` s dedup), `rules.yaml` engine a IFTTT Webhooks klient. Přidej `deploy/docker-compose.yml` a Caddy snippet pro `promo.ol1n.com`. Žádný Python ani Bash skripty — jen Go a Makefile. Na konci napiš `RUNBOOK.md` s kroky, co musím ručně udělat v Apify a IFTTT konzoli.
