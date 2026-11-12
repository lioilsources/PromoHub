# 01-ACCOUNTS_PLAN.md — Účty na všech promo platformách: přidání a využití

Handoff pro Claude Code (Opus). Navazuje na `00-PROMO_HUB_PLAN.md` (dál „00“) a na hotovou fázi 1. Platí pravidla z 00: Go, žádný Python ani Bash, identifikátory anglicky, nic se nepublikuje bez schválení v Telegramu.

## 0. TL;DR

- Hub dnes umí jen Apify → pravidla → IFTTT/Telegram/Discord. Tenhle plán z něj dělá správce **všech promo účtů**: každý účet má cestu pro čtení (metriky, komentáře, zmínky) a cestu pro publikaci.
- 16 platforem (§3). Navíc proti zadání: **Threads, LinkedIn, Pinterest, Telegram kanál, Discord, Hacker News, Product Hunt, dev.to**. Pixelfed a další fediverse jdou zadarmo přes Mastodon adaptér.
- Každý účet funguje od prvního dne. Čtení: `api → apify → none`. Publikace: `api → ifttt → manual`. Když platforma API nedá (audit, review), účet spadne o stupeň níž a práce pokračuje.
- 5 vln, každá je jeden PR a hub po každé běží. Vlna 0 jsou ruční kroky Olina a běží souběžně.

## 1. Rozsah

- **Nahrazuje z 00** v části sociálních účtů: fázi 2 (collectory YouTube/Bluesky/Reddit, scheduler), fázi 3 (composer, schvalování, publikace), fázi 4 (Instagram) a fázi 5 (TikTok, Shorts, Threads, LinkedIn).
- **Mimo rozsah** (zůstává v 00 fázi 2, dostane vlastní plán): recenze App Store a Google Play, `/scan`, denní digest.
- **Non-goals:** vlastní scrapery, publikace bez schválení, automatické odpovědi, transkódování videa, placená reklama, víc uživatelů hubu.

## 2. Stav API k 2026-11-12 — co se liší od 00

Rešerše z webu, většinou sekundární zdroje. Než začneš psát adaptér, ověř to v oficiální dokumentaci (brána v §4.6).

| Platforma | Zjištění | Dopad |
|---|---|---|
| X | Free tier skončil v 02/2026, API je pay-per-use (řádově $0,015 za post, post s odkazem výrazně dráž, čtení $0,005 za post). | Čtení dál Apify, publikace dál IFTTT. Přímé API jen jako placená záloha (§13/7). |
| Reddit | Od 11/2025 nejde API aplikaci založit samoobslužně (Responsible Builder Policy); osobní skripty bývají zamítnuté. | „Reddit OAuth zdarma“ z 00 neplatí. Čtení Apify, publikace ručně. |
| Bluesky | `searchPosts` na `public.api.bsky.app` vrací 403. | Hledej přes PDS s přihlášenou session. |
| Instagram | „Instagram API with Instagram Login“ nechce FB Page; insights mají metriku `views`. | Stačí professional účet, fáze 4 z 00 je jednodušší. |
| Facebook | Posty z aplikace v Development módu vidí jen lidé s rolí v aplikaci. | Publikace přes IFTTT, Graph API jen na čtení. |
| YouTube | Upload z neauditovaného projektu se zamkne jako private. | Publikace ručně, `videos.insert` až po auditu. |
| TikTok | Bez auditu je Direct Post jen private. Upload do inboxu (draft, dokončí se v appce) jde v sandboxu. | Publikace = draft do inboxu. |
| Pinterest | Trial přístup vytváří piny viditelné jen autorovi. | Publikace přes IFTTT. |
| LinkedIn | Postování přes `w_member_social` jde; metriky osobního profilu jsou za schválením. | Publikace IFTTT, čtení žádné. |
| Threads | Vlastní účet jako tester publikuje bez App Review. | Přímé API, IFTTT jako záloha. |
| IFTTT | Má akce pro X, Facebook Pages, LinkedIn, Threads, Mastodon a Pinterest. Ceník se na podzim 2026 možná změnil na kredity. | Univerzální záloha publikace; tarif ověřit (§13/4). |

## 3. Platformy

### 3.1 Cesty (výchozí hodnoty v registru platforem)

| # | Platforma | Čtení: metriky a moje posty | Příchozí: komentáře, zmínky | Publikace teď → později | Přihlášení |
|---|---|---|---|---|---|
| 1 | Bluesky | api: `getProfile`, `getAuthorFeed` (veřejný AppView) | api: `listNotifications`, `searchPosts` (PDS) | api: `createRecord` + `uploadBlob` | app password v `.env` |
| 2 | Mastodon (+ Pixelfed, GoToSocial, Akkoma) | api: `verify_credentials`, `accounts/:id/statuses` | api: `notifications?types[]=mention`, `timelines/tag/:tag` | api: `statuses` + `media`, hlavička `Idempotency-Key` | token aplikace z nastavení instance, `.env` |
| 3 | X | apify `x-profile` (hotovo) | apify `x-mentions` (hotovo) | ifttt `publish_x` | IFTTT |
| 4 | Threads | api: `threads`, `insights`, `threads_insights` | api: `replies`, `mentions`, `keyword_search` (malá kvóta) | api: kontejner → `threads_publish`; záloha ifttt | OAuth, token 60 dní s obnovou |
| 5 | Instagram | api: `me/media`, `insights`; do té doby apify `ig-profile` | api: `comments`; zmínky ověřit, jinak apify | api: kontejner → `media_publish` (obrázek, carousel, Reels) | OAuth, token 60 dní s obnovou |
| 6 | Facebook Page | api: `/{page}`, `/{page}/posts`, insights | api: `/{post}/comments` | ifttt `publish_facebook` → api, až bude aplikace Live | OAuth → Page token |
| 7 | YouTube | api s API klíčem: `channels`, `playlistItems`, `videos`; Analytics přes OAuth (vlna 4) | api: `commentThreads?allThreadsRelatedToChannelId`; apify `yt-mentions` | manual → api po auditu | API klíč; OAuth refresh token |
| 8 | TikTok | apify `tiktok-profile` (hotovo) | apify (volitelně komentáře) | api: draft do inboxu (`video.upload`) nebo manual → Direct Post po auditu | OAuth: access 24 h, refresh 365 dní |
| 9 | Reddit | apify, nový kind `reddit-profile` | apify `reddit-mentions` (hotovo) | manual (odkaz na submit formulář) | žádné |
| 10 | LinkedIn | none | none | ifttt `publish_linkedin` → api `w_member_social` | IFTTT |
| 11 | Pinterest | api v Trial režimu (ověřit), jinak apify | — | ifttt `publish_pinterest` → api po Standard access | IFTTT |
| 12 | Telegram kanál | api: `getChatMemberCount` | — | api: Bot API (`sendMessage`, `sendPhoto`, `sendVideo`) | bot token (už je) |
| 13 | Discord server | api: `GET /invites/{code}?with_counts=true` | — | api: webhook s `?wait=true` | webhook URL |
| 14 | Hacker News | api: Firebase `user/<id>`, `item/<id>` | api: Algolia `search_by_date` | manual | — |
| 15 | Product Hunt | api: GraphQL v2 (hlasy, komentáře mých produktů) | api: komentáře | manual | developer token |
| 16 | dev.to | api: `articles/me` | api: `comments?a_id=` | api: `POST /api/articles` s `canonical_url` | API klíč |

**Zamítnuto:** Snapchat a WhatsApp kanály (žádné API pro organický obsah), Twitch a Kick (živé vysílání), Medium (API zavřené), Tumblr, Quora, Nostr a Farcaster (malý dosah pro dětské appky), FB skupiny (API zrušeno 2024, jdou jen přes `manual`). Kterákoli se dá později přidat jako další adaptér.

### 3.2 Formát a použití (výchozí limity; hodnoty ověř)

| Platforma | Text | Média a odkaz | Co tam dávat |
|---|---|---|---|
| Bluesky | 300 grafémů | 4 obrázky nebo 1 video; odkaz jako link card (`embed.external`), hashtagy a odkazy přes facets | dev-log, novinky |
| Mastodon | 500 znaků (čti z `/api/v2/instance`), URL se počítá za 23 | 4 média, alt text povinně; hashtagy jsou hlavní cesta k dosahu | dev-log, indie komunita |
| X | 280 vážených znaků, URL za 23 | 4 obrázky | krátké novinky, odpovědi na zmínky |
| Threads | 500 znaků | obrázek, video, carousel, `link_attachment` | konverzační posty |
| Instagram | 2200 znaků, hashtagů ≤ 5 | obrázek nebo Reels 9:16; odkaz v textu není klikací → „link in bio“ | vizuál, Reels pro rodiče |
| Facebook Page | dlouhý text | link post s náhledem, fotka z URL | rodiče (CZ), oznámení |
| YouTube | titulek 100, popis 5000 | Shorts 9:16 do 3 min | ukázky appek |
| TikTok | popisek 2200 | video 9:16 | krátká videa |
| Reddit | titulek 300 | pravidla self-promo každého subredditu | jen hodnotné posty, ručně |
| LinkedIn | 3000 znaků | odkaz, obrázek | build in public |
| Pinterest | titulek 100, popis 800 | obrázek 2:3 + odkaz, nástěnka per appka | evergreen pro rodiče |
| Telegram | 4096, popisek média 1024 | fotka, video do 50 MB | oznámení stávajícím fanouškům |
| Discord | 2000 | embed | oznámení komunitě |
| dev.to | markdown článek | `canonical_url` na blog | technické články |

Délku počítej jednotkou platformy: Bluesky grafémy (`github.com/rivo/uniseg`), facets v bajtových offsetech UTF-8, X vážené znaky.

## 4. Zásady návrhu

1. **Registr platforem** (`internal/platform`) je jediné místo, kde žijí limity, schopnosti (`Profile`, `Inbound`, `Search`, `Publish`, `Reply`), výchozí cesty, intent URL a parser URL → `external_id`. `make check` proti němu validuje `accounts.yaml`.
2. **Jeden balík na platformu:** `internal/platforms/<name>/` s `client.go`, `collect.go`, `publish.go`. Klient a přihlášení se sdílí mezi čtením a publikací. Odchylka od 00 §6 (ten měl `collectors/<name>` a jeden `publisher`); `collectors/apifysrc` zůstává.
3. **Degradace místo čekání:** když brána ověření (bod 6) zjistí, že cesta `api` není dostupná, přepni výchozí cestu v registru na `ifttt`/`apify`/`manual`, zapiš to do `docs/PLATFORMS.md` a pokračuj.
4. **Kanonické `external_id`** per platforma, stejné pro všechny cesty (API, Apify, IFTTT, ruční URL), jinak vzniknou duplicity v `content_items`. Instagram a Threads: shortcode. YouTube: video ID. Bluesky: `<did>/<rkey>`. Mastodon: `<instance>/<id>`. Reddit: base36 bez prefixu `t3_`. `apifysrc.mapIGPost` dnes bere `id` před `shortCode`; otoč to teď, dokud není nic nasazené.
5. **Adaptér nezná DB.** Vrací `collectors.Batch` a nový kurzor; ukládá jen `hub.Ingest` (`internal/hub/ingest.go`). `Source.RunID` nastav na `<capability>:<account_id>:<unix slotu>`, aby unikátní index na `metric_snapshots.source_run_id` chránil i před dvojím během plánovače.
6. **Brána ověření:** před každým adaptérem přečti oficiální dokumentaci endpointů z §3.1 a zapiš do `docs/PLATFORMS.md` řádek: datum, endpointy, limity, přihlášení, rozdíl proti plánu, zvolená cesta.
7. **Fixture z reálné odpovědi** ke každému mapování, jako u `apifysrc/real_test.go`. Získáš ji příkazem `promohub probe` (§6). Ořež ji na čtená pole a odstraň z ní tokeny; repo je veřejné.
8. **Tiché selhání je chyba.** Prázdná úspěšná odpověď neznamená nula zmínek (zkušenost s `trudax/reddit-scraper`). Collector, který selže třikrát po sobě nebo vrátí prázdný profil, vyvolá trigger `account.error`.

## 5. Konfigurace: `accounts.yaml`

Nová pole jsou volitelná, dnešní soubor zůstává platný. Výchozí `read`/`publish` bere hub z registru.

```yaml
accounts:
  - platform: bluesky
    handle: TODO.bsky.social
    auth_ref: BLUESKY_MAIN        # prefix proměnných v .env (BLUESKY_MAIN_APP_PASSWORD); "oauth" = token v DB
    lang: en                      # jazyk postů; "any" bere všechny
    apps: []                      # prázdné = všechny appky z apps.yaml
    read: api                     # api | apify | none
    publish: api                  # api | ifttt | manual | none
    max_per_day: 5
  - platform: mastodon
    handle: TODO@mastodon.social  # user@instance; instance se bere z handle
    auth_ref: MASTODON_MAIN
    hashtags: [indiedev, kidsapps] # sledované přes timelines/tag
  - platform: x
    handle: TODO
    publish: ifttt
    ifttt_event: publish_x
  - platform: pinterest
    handle: TODO
    boards: {swypekids: TODO_BOARD}
```

Kampaň pro appku `A` v jazyce `L` vytvoří draft pro každý účet, kde `publish != none`, `apps` je prázdné nebo obsahuje `A`, a `lang` je `L` nebo `any`. `apps.yaml` dostane volitelné `hashtags:` per appka.

Secrets jen v `.env` (statické) nebo šifrovaně v DB (OAuth). Do YAML nikdy.

## 6. Kód: co přibude a na co se napojit

| Balík | Obsah |
|---|---|
| `internal/platform` | registr (§4.1) |
| `internal/platforms/<name>` | klient, `Collector`, `Publisher`, případně `Replier` |
| `internal/creds` | statické secrets podle `auth_ref`; OAuth tokeny v tabulce `credentials`, AES-256-GCM, klíč `PROMOHUB_SECRET_KEY`, AAD = id účtu; obnova tokenů |
| `internal/oauth` | `GET /oauth/{platform}/start` a `/callback`, podepsaný `state` (HMAC, platnost 10 min) |
| `internal/scheduler` | `time.Ticker` + jitter ±10 % nad tabulkou `collector_state`; backoff do 6 h; respektuje `Retry-After` a rate-limit hlavičky |
| `internal/telegram` | long polling `getUpdates`, příkazy, karty, ForceReply; `internal/notify` zůstává pro odesílání |
| `internal/composer` | varianty textu per účet (§8.2) |
| `internal/publisher` | worker a stavový automat (§8.3), cesty `ifttt` a `manual` |
| `internal/media` | úložiště souborů, `GET /media/{token}`, import |

Napojení na stávající kód:

- `config.Account` (`internal/config/config.go`) rozšiř o pole z §5; `Files.validate` volá registr.
- `collectors.Collector` (`internal/collectors/collectors.go`) rozšiř na `Collect(ctx, acct, capability, cursor) (Batch, cursor, error)`.
- `hub.Run` (`internal/hub/hub.go`) dostane další smyčky: scheduler, publisher, telegram, obnova tokenů.
- `rules`: nové triggery `publish.done`, `publish.failed`, `account.error`, `account.auth_expiring`; proměnná `account`.
- `ifttt.Client` dostane `TriggerValues(event, v1, v2, v3)` na klasický endpoint `/trigger/{event}/with/key/{key}`. Applety pak berou `Value1` = text, `Value2` = odkaz, `Value3` = URL obrázku a nepotřebují filter code.
- `internal/hub/http.go`: nové cesty `/oauth/*` a `/media/*`; v `deploy/Caddyfile.snippet` je zveřejni a z access logu vyřaď `code` a `state` stejně jako `secret`.
- `deploy/docker-compose.yml`: volume `/data/media`. Image je distroless nonroot (uid 65532), nastav vlastníka volume.
- Podpříkazy v `cmd/promohub/main.go`:
  - `accounts` — tabulka účtů: cesty, stav přihlášení, expirace, poslední úspěšné čtení a publikace;
  - `probe <platform:handle> [capability]` — zavolá adaptér a vypíše `Batch` jako JSON, nic neukládá;
  - `connect <platform:handle>` — vypíše OAuth odkaz;
  - `media import --name <file>` — čte soubor ze stdin. Makefile cíl `media FILE=…` ho pošle přes `ssh joda docker exec -i promohub …`, takže není potřeba žádný admin endpoint.
- Metriky: `promohub_collect_total{platform,capability,result}`, `promohub_publish_total{platform,via,result}`, `promohub_token_expiry_seconds{account}`.

## 7. Datový model

Nové migrace, `000001` needituj. Sloupce v `accounts` přidávej jen ty, které potřebuje dotaz; zbytek žije v YAML.

- `collector_state(account_id, capability, cursor jsonb, next_run_at, last_ok_at, fail_count, last_error)` — PK `(account_id, capability)`. Je to zároveň rozvrh.
- `credentials(account_id PK, kind, ciphertext, nonce, scopes, expires_at, refresh_expires_at, updated_at, last_error)`.
- `campaigns(id, app_id, source_content_item_id, lang, title, body, link, media jsonb, status, created_at)` — jedno sdělení, které má jít ven.
- `drafts(id, campaign_id, account_id, via, text, extra jsonb, media jsonb, status, scheduled_at, attempt_key, published_external_id, published_url, content_item_id, error, updated_at)` — `UNIQUE(campaign_id, account_id)`. `extra` drží titulek, subreddit, nástěnku, alt texty.
- `media_assets(id, token UNIQUE, path, mime, bytes, sha256, width, height, duration_ms, created_at, expires_at)`.
- `kv(key PK, value)` — offset Telegram `getUpdates`, rozpracované editace.
- `inbound_items` dostane `account_id` a `replied_at` (vlna 5).

## 8. Publikační tok

### 8.1 Zdroje kampaně

- `/post` v Telegramu: text, volitelně odkaz, appka, jazyk a přiložené médium.
- `content.new` z IFTTT (`youtube_new_video` už existuje) → hub navrhne kampaň „nové video“.
- RSS blogu (vlna 5, volitelné).

### 8.2 Composer

- **v0, deterministický:** vezme text kampaně, přidá odkaz podle pravidel platformy (inline, link card, „link in bio“), hashtagy z `apps.yaml` a ořízne na limit. Funguje vždy.
- **v1, LLM:** jedno volání na OpenAI-kompatibilní gateway (`LLM_BASE_URL`, `LLM_MODEL`, `LLM_API_KEY` v `.env`), výstup striktní JSON `{account_key: text}`, styl z `voice.md`. Neplatný výstup → jeden retry → v0. Timeout 60 s → v0. Composer nikdy neblokuje publikaci.
- Vstupem promptu je jen můj vlastní obsah. Text z `inbound_items` do promptu nesmí (prompt injection přes komentáře).
- Odkazy na vlastní web dostanou `utm_source=<platform>&utm_medium=social&utm_campaign=<slug kampaně>`.

### 8.3 Stavový automat `drafts`

```
draft ─▶ approved ─▶ publishing ─▶ published      (mám external_id nebo URL)
  │                      ├──────▶ sent            (předáno IFTTT / do TikTok inboxu / ruční karta)
  └▶ skipped             ├──────▶ failed          (odmítnuto před odesláním; lze znovu schválit)
                         └──────▶ unknown         (timeout nebo pád po odeslání)
sent ─▶ published        (spárováno s content_items nebo Olin poslal URL)
```

- Worker bere drafty přes `FOR UPDATE SKIP LOCKED`, při převzetí zapíše `attempt_key`.
- **Nikdy nepostuj dvakrát.** Draft, který po startu zůstal v `publishing`, jde do `unknown` a Telegram se zeptá: „Vyšlo to? [Ano + URL] [Ne, zkus znovu]“. Automatický retry jen tam, kde platforma zná idempotenční klíč (Mastodon).
- `sent → published`: po dalším čtení účtu spáruj nový `content_item` téhož účtu z následujících 30 minut podle normalizovaného začátku textu. U účtů se čtením přes Apify spusť po `sent` jejich profile task (za 10 minut, nejvýš jednou za hodinu).
- Draft přes limit platformy nejde schválit; karta ukáže `312/300`.
- `max_per_day` účtu se kontroluje při schválení.

### 8.4 Telegram

- Jen chaty z `TELEGRAM_ADMIN_IDS`; ostatní update zahoď a zaloguj.
- Kampaň = hlavička s „Schválit vše“ + jedna zpráva na draft s tlačítky ✅ Schválit / ✏️ Upravit / ⏭ Přeskočit / 🕒 Naplánovat.
- `callback_data` má limit 64 bajtů: posílej jen `d:<id>:<akce>`.
- Upravit = ForceReply; odpověď přepíše text a znovu ověří limit.
- Cizí text (komentáře, zmínky) posílej bez `parse_mode`.
- **Cesta `manual`:** karta s textem v monospace (ťuknutím se kopíruje), médiem jako přílohou a tlačítkem s intent URL (Reddit `submit?title=…`, HN `submitlink?u=…&t=…`, X `intent/tweet`, LinkedIn `sharing/share-offsite`, Pinterest `pin/create/button`). Tlačítko „Hotovo“ si vyžádá URL postu; hub z ní udělá `content_item` a začne ho sledovat.
- Soubory z Telegramu jdou přes Bot API stáhnout jen do 20 MB. Větší video nahraj přes `make media`.

### 8.5 Média

- `GET /media/{token}`: token 32 náhodných bajtů, expirace 24 h po posledním plánovaném použití, správný `Content-Type`, podpora `Range` (`http.ServeContent`). Instagram, Threads, Facebook a IFTTT si média stahují z veřejné URL.
- Hub nic netranskóduje. Ověří velikost a u MP4 délku a rozměry proti registru; nevyhovující soubor odmítne už při tvorbě draftu.

### 8.6 OAuth

- `/connect <platform:handle>` v Telegramu (nebo podpříkaz `connect`) vrátí odkaz na `/oauth/{platform}/start?state=…`. Callback uloží tokeny šifrovaně.
- Redirect URI: `https://promo.ol1n.com/oauth/<platform>/callback`.
- Obnova jednou denně: Instagram a Threads, když zbývá méně než 7 dní; TikTok před voláním; Google standardní refresh token.
- Past: Google OAuth aplikace ve stavu „Testing“ vydává refresh tokeny jen na 7 dní. Consent screen musí být „In production“.
- Selhání obnovy nebo expirace do 7 dní → `account.auth_expiring` → Telegram.

## 9. Vlny

Na začátku každé vlny udělej společný commit s rozhraními a migrací. Adaptéry jsou pak nezávislé balíky a jdou rozdat subagentům; ti testy píšou, ale nespouštějí. Gate běží jednou na konci vlny: `make vet test check` a `make test-db`. Po každé vlně doplň `RUNBOOK.md`, `docs/PLATFORMS.md`, `.env.example` a `accounts.yaml` (placeholdery `TODO_…`).

### Vlna 1 — registr, plánovač, čtení bez review
- Registr platforem, rozšířený `accounts.yaml`, `creds` pro statické secrets, `collector_state`, scheduler, `accounts`, `probe`, `account.error`.
- Adaptéry čtení: Bluesky, Mastodon, YouTube (API klíč), počty Telegram kanálu a Discordu.
- Sjednoť `external_id` (§4.4). Založ `CLAUDE.md` repa (do 80 řádků: gate, struktura, odkazy do `docs/`).
- ✅ Do 24 h jsou v `metric_snapshots` účtové i postové snapshoty z Bluesky, Mastodonu a YouTube bez Apify. Zmínka na Bluesky nebo Mastodonu dorazí do Telegramu do 15 minut a jen jednou. Restart hubu nevyrobí duplicity. `promohub accounts` ukáže stav všech účtů.

### Vlna 2 — publikace: jádro a snadné cíle
- `campaigns`, `drafts`, `media_assets`, `kv`; `media`; Telegram bot; composer v0 (v1, pokud je gateway dostupná); publisher.
- Cesty: `api` pro Bluesky, Mastodon, Telegram kanál, Discord; `ifttt` pro X, Facebook Page, LinkedIn, Pinterest (a Threads jako dočasnou); `manual` pro všechno ostatní.
- Párování `sent → published`, triggery `publish.*`.
- ✅ Jeden `/post` s obrázkem → po schválení vyjde na Bluesky (link card), Mastodonu (alt text) a v Telegram kanálu; X přes IFTTT; Reddit dorazí jako ruční karta. Zabití procesu uprostřed publikace neskončí dvojím postem (integrační test). Publikované posty se do 24 h objeví v metrikách.

### Vlna 3 — OAuth a Meta
- `credentials`, `oauth`, obnova tokenů, `/connect`.
- Instagram: čtení, komentáře, publikace obrázku a carouselu. Threads: čtení, replies, mentions, publikace. Facebook Page: čtení a komentáře; publikace zůstává na IFTTT.
- Po zapnutí IG API vypni Apify task `ig-profile`.
- ✅ Kampaň vyjde na Instagramu a Threads ze schválení v Telegramu. Token se sám obnoví (test s falešným časem). Komentář na IG nebo FB dorazí do Telegramu.

### Vlna 4 — video a účty s auditem
- Kontrola videa v `media`; video na Bluesky, Mastodon, Threads, Instagram Reels, Telegram.
- TikTok: OAuth a draft do inboxu (`FILE_UPLOAD`, bez ověřování domény) → stav `sent` a zpráva „dokonči v TikToku“ s popiskem ke zkopírování.
- YouTube: OAuth, Analytics (impressions, CTR, watch time do `extra`); upload zůstává `manual`, `videos.insert` je za `publish: api` a zapne se až po auditu.
- ✅ Jedno vertikální video → Reels a Threads publikované, TikTok draft v inboxu, YouTube Shorts jako ruční karta s titulkem a popisem.

### Vlna 5 — dlouhý ocas, odpovědi, přehled
- Hacker News, Product Hunt, dev.to (čtení; dev.to i publikace článku).
- Nové kindy v `apifysrc`: `reddit-profile`, volitelně `pinterest-profile` a `tiktok-comments`. Každý jen s fixture z reálného běhu.
- Odpověď z Telegramu: tlačítko ↩️ u příchozí položky → ForceReply → `Replier` (Bluesky, Mastodon, Threads, Instagram, Facebook, YouTube). Napsal ji Olin, další schválení není potřeba.
- `/stats`: tabulka per účet — followers Δ7 d a Δ30 d, počet postů, engagement na post. Smysl: vidět, která platforma se vyplácí.
- ✅ Zmínka appky na HN dorazí do Telegramu. Odpověď z Telegramu vyjde na Bluesky. `/stats` porovná všechny účty.

## 10. Vlna 0 — ruční kroky Olina (souběžně s vlnou 1)

Stejný handle všude, kde je volný. Výsledkem každého kroku je handle v `accounts.yaml` a secret v `.env` na JODĚ.

| Platforma | Co založit | Co předat hubu |
|---|---|---|
| Bluesky | účet; volitelně handle na vlastní doméně | App password |
| Mastodon | účet na zvolené instanci (§13/3); zapnout zahrnutí do vyhledávání | Development → New application (`read`, `write:statuses`, `write:media`) → access token |
| X | účet | propojit v IFTTT, applet `publish_x` |
| Meta | Instagram přepnout na professional, FB Page, Threads profil; jedna Meta aplikace s use casy Threads API a Instagram API; přidat se jako tester a pozvánky přijmout v appkách | App ID a secret; redirect URI; FB Page propojit v IFTTT |
| YouTube | kanál; Google Cloud projekt s YouTube Data API v3 a Analytics API | API klíč omezený na ta API; OAuth klient (web), consent screen „In production“ |
| TikTok | účet; developer aplikace s Login Kit a Content Posting API, sandbox, vlastní účet jako target user | client key a secret; redirect URI |
| Reddit | účet (stáří a karma rozhodují, jestli post projde) | jen handle |
| LinkedIn | profil | propojit v IFTTT, applet `publish_linkedin` |
| Pinterest | business účet, nástěnka per appka | propojit v IFTTT, applet `publish_pinterest` |
| Telegram | veřejný kanál, bot jako admin | ID kanálu |
| Discord | kanál oznámení, trvalá pozvánka | webhook URL, kód pozvánky |
| HN, Product Hunt, dev.to | účty | uživatelské jméno; developer token; API klíč |

IFTTT applety `publish_*`: trigger Webhooks „Receive a web request“ (ne JSON varianta), akce dané služby s poli `Value1` = text, `Value2` = odkaz, `Value3` = URL obrázku. Notifikační applety z 00 §8 (1–5) jdou nahradit přímými sinky `telegram:` a `discord:`, kdyby docházel limit appletů.

## 11. Bezpečnost

- Publikuje se jen draft ve stavu `approved`, schválený callbackem z `TELEGRAM_ADMIN_IDS`. IFTTT inbound zůstává jen spouštěč (00 §12).
- Tokeny nikdy do logu ani do chybové hlášky. Kde to API dovolí, `Authorization: Bearer` místo tokenu v URL; jinak vzor `redact` z `internal/notify`.
- OAuth `state` podepsaný a s expirací; `/oauth/start` bez platného `state` vrací 404.
- `/media/{token}` jen s neuhodnutelným tokenem a expirací; žádný výpis adresáře.
- Veřejné repo: fixtures bez tokenů a soukromých dat, žádné adresy ani cesty strojů.
- SPARK: hub volá jen gateway z `.env`. Na SPARKu nic nespouštěj ani nezastavuj; kapacitu řeší broker a Olin (§13/8).

## 12. Testy

- Bez sítě a bez `time.Now`: každý klient má `BaseURL`, `*http.Client` a `now func() time.Time`; testy proti `httptest.Server`.
- Per adaptér: mapování z reálné fixture, stránkování a kurzor, 429 s `Retry-After`, expirovaný token.
- Publisher: idempotence (pád v `publishing`), limit délky v jednotkách platformy, facets u Bluesky s diakritikou a emoji.
- Integrační testy s Postgresem jako dnes přes `PROMOHUB_TEST_DATABASE_URL`.

## 13. Otevřené otázky (výchozí volba platí, dokud Olin neřekne jinak)

1. Jeden brand účet na platformu, nebo účty per appka? **Výchozí: jeden brand účet;** pole `apps` umožní rozdělení později.
2. Jazyk: **výchozí EN.** Česká kampaň (Hajánek) jde jen na účty s `lang: cs` nebo `any`; které to budou, určí Olin (nejspíš FB Page).
3. Instance Mastodonu: **výchozí `mastodon.social`.**
4. IFTTT tarif a limit appletů po změně ceníku: ověřit; potřeba je asi 10 appletů.
5. Žádat o audity (YouTube API, TikTok, Pinterest Standard, Meta Live)? **Výchozí: ne.** Rozhodnout po měsíci provozu podle `/stats`.
6. Má Olin starší schválenou Reddit API aplikaci? Pokud ano, přidá se cesta `api`; jinak se s ní nepočítá.
7. X API jako placená záloha publikace (odpovědi na tweety, které IFTTT neumí)? **Výchozí: neimplementovat.**
8. Jak hub dostane LLM kapacitu na SPARKu (stálá služba za gateway, nebo okno přes broker)? Do rozhodnutí běží composer v0.
9. Je OrbitronTactics na Steamu nebo itch.io? Pokud ano, přidat adaptéry (Steam má veřejné JSON recenze, itch.io API klíč).

Otázky z 00 §13 platí dál, kromě č. 1 (FB Page už pro Instagram není potřeba).

## 14. Prompt pro Opus

> Přečti `Prompts/00-PROMO_HUB_PLAN.md` a `Prompts/01-ACCOUNTS_PLAN.md`. Implementuj **vlnu N** z §9 plánu 01 jako jeden PR. Než napíšeš adaptér, projdi bránu ověření (§4.6) a výsledek zapiš do `docs/PLATFORMS.md`; když cesta `api` není dostupná, degraduj podle §4.3 a pokračuj. Drž zásady §4 a stavový automat §8.3. Žádný Python ani Bash. Na konci spusť gate, doplň `RUNBOOK.md` o ruční kroky a vypiš, co z §13 potřebuješ rozhodnout.
