# Platformy — záznamy brány ověření

Záznam brány ověření z `Prompts/01-ACCOUNTS_PLAN.md` §4.6. Ke každému adaptéru jeden řádek: kdy, jaké endpointy, limity a přihlášení, co se liší od plánu a jakou cestu hub zvolil.

Legenda zdroje:

- **živě** — ověřeno voláním skutečného API, odpověď je ve fixtures testů;
- **dok** — podle oficiální dokumentace, bez živého volání (chybí přístup).

## Vlna 1 — čtení

| Platforma | Datum | Endpointy | Přihlášení | Zdroj | Rozdíl proti plánu / rozhodnutí |
|---|---|---|---|---|---|
| Bluesky | 2026-11-12 | `app.bsky.actor.getProfile`, `app.bsky.feed.getAuthorFeed` (`filter=posts_no_replies`) na `public.api.bsky.app` | žádné | živě | Feed obsahuje reposty (`reason` = `reasonRepost`), adaptér je přeskakuje. `bookmarkCount` je nové pole, ukládá se do `extra`. |
| Bluesky | 2026-11-12 | `com.atproto.server.createSession`, `app.bsky.notification.listNotifications` (`reasons=mention,reply,quote`), `app.bsky.feed.searchPosts` přes PDS | app password | dok | Search jde přes PDS se session, ne přes public AppView (ten vrací 403). Session se obnovuje po 90 min novým přihlášením, bez refresh tokenu. |
| Mastodon | 2026-11-12 | `GET /api/v1/accounts/lookup`, `/api/v1/accounts/:id/statuses`, `/api/v1/timelines/tag/:tag` | žádné | živě | `quotes_count` existuje (Mastodon 4.5). Limity instance: `GET /api/v2/instance` → `configuration.statuses` (500 znaků, 4 média, URL = 23). |
| Mastodon | 2026-11-12 | `GET /api/v1/notifications?types[]=mention&since_id=` | token aplikace | dok | Odpověď na můj post = `in_reply_to_account_id` je moje ID. Inbound ID má tvar `<instance>/<status id>`, protože ID statusů jsou lokální pro instanci. |
| YouTube | 2026-11-12 | `channels.list` (`forHandle` nebo `id`), `playlistItems.list` (uploads), `videos.list`, `commentThreads.list?allThreadsRelatedToChannelId` | API klíč | dok | Každé volání stojí 1 jednotku; profil 3 jednotky, komentáře 1 jednotku. Při intervalu 3 h / 15 min je to ~120 jednotek denně z 10 000. Upload zůstává `manual`. |
| Telegram | 2026-11-12 | Bot API `getChatMemberCount` | bot token (admin kanálu) | dok | — |
| Discord | 2026-11-12 | `GET /invites/{code}?with_counts=true` | žádné | živě | `approximate_presence_count` jde do `extra.online`. |

## Kanonické external_id

Implementace je `internal/platform.ExternalID`. Stejné ID dávají API, Apify, IFTTT i vložená URL.

| Platforma | ID | Poznámka |
|---|---|---|
| Bluesky | record key (`3n22yg6lg2k2f`) | Plán uváděl `<did>/<rkey>`. Rkey stačí, protože `content_items` drží jen moje posty, a zůstává kompatibilní s IFTTT z fáze 1. Inbound používá celé `at://` URI. |
| Mastodon | ID statusu | Inbound `<instance>/<id>`. |
| Instagram, Threads | shortcode | Apify mapování přepnuto z číselného `id` na `shortCode`. |
| YouTube | video ID | |
| X | ID statusu | |
| TikTok | video ID | |
| Reddit | base36 ID postu | |

## Vlna 3 — OAuth a Meta

| Platforma | Datum | Endpointy | Přihlášení | Zdroj | Rozdíl proti plánu / rozhodnutí |
|---|---|---|---|---|---|
| Instagram | 2026-11-12 | `graph.instagram.com/v24.0`: `/me`, `/me/media`, `/{media}/insights` (`views,reach,saved,shares`), `/{media}/comments`, `/me/media` → `/me/media_publish` | OAuth Instagram Login: krátký token → 60denní, refresh přes `ig_refresh_token` | dok | Zmínky (@tag) API bez webhooků nedává, inbound jsou jen komentáře. Text bez média nejde (Reject). Insights se čtou jen pro 10 nejnovějších postů. |
| Threads | 2026-11-12 | `graph.threads.net/v1.0`: `/me/threads`, `/me/threads_insights` (`followers_count`), `/{id}/insights`, `/me/mentions`, `/{id}/replies`, `/me/threads` → `/me/threads_publish` | OAuth Threads: 60denní token, `th_refresh_token` | dok | Textový post nese `link_attachment`. `REPOST_FACADE` se přeskakuje. `keyword_search` zatím ne (malá kvóta). |
| Facebook Page | 2026-11-12 | `graph.facebook.com/v24.0`: `/{page}` (`followers_count,fan_count`), `/{page}/posts`, `/{post}/comments`, publikace `/{page}/feed` a `/{page}/photos` | Facebook Login → dlouhodobý user token → page token (neexpiruje) | dok | Výchozí publikace zůstává IFTTT: v Development módu posty aplikace vidí jen role aplikace. Cesta `api` je připravená pro Live. |
| OAuth obecně | 2026-11-12 | `/oauth/{platform}/start`, `/callback`, `/deauthorize` | `state` podepsaný HMAC, platnost 10 min | — | Tokeny jsou šifrované AES-256-GCM s AAD na účet. Refresh se dělá líně při použití a jednou za 6 h; varování `account.auth_expiring` max. 1× denně. |

## Vlna 4 — video

| Platforma | Datum | Endpointy | Přihlášení | Zdroj | Rozdíl proti plánu / rozhodnutí |
|---|---|---|---|---|---|
| Bluesky | 2026-11-12 | `uploadBlob` (video/mp4) + `app.bsky.embed.video` | app password | dok | Bez video služby `video.bsky.app`: blob jde přímo na PDS, zpracuje ho AppView. Limit 100 MB / 3 min, ověřit, zdroje se rozcházejí. |
| Mastodon | 2026-11-12 | `POST /api/v2/media` (streamovaně), poll `GET /api/v1/media/:id` až 3 min | token | dok | Limit instance `configuration.media_attachments.video_size_limit` (~99 MB). |
| Telegram | 2026-11-12 | `sendVideo` (multipart, `supports_streaming`) | bot | dok | Bot smí poslat max. 50 MB. |
| Instagram / Threads | 2026-11-12 | kontejner `REELS` / `VIDEO` s `video_url` (`/media/{token}`), čekání na `FINISHED` | OAuth | dok | Hub nic netranskóduje. Při chybě kontejneru je to Reject, protože před publish ještě nic není veřejné. |
| TikTok | 2026-11-12 | `/v2/user/info/`, `/v2/video/list/`, `/v2/post/publish/inbox/video/init/` (`FILE_UPLOAD`) + PUT po chuncích (≤ 64 MB jeden, jinak 10 MB a zbytek do posledního) | OAuth Login Kit (access 24 h, refresh 365 dní) | dok | Draft přijde do inboxu, publikace se dokončí v appce. Stav je `sent` a bot pošle popisek ke zkopírování. Direct Post až po auditu. `read: api` volitelně místo Apify. |
| YouTube | 2026-11-12 | YouTube Analytics `reports` (`views,estimatedMinutesWatched,averageViewDuration,subscribersGained`, 28 dní, per video) | OAuth Google | dok | Impressions a CTR v API nejsou, místo nich watch time. `videos.insert` (resumable) je hotový, ale jen pro `publish: api` po auditu projektu. |
| Video obecně | 2026-11-12 | — | — | — | MP4/MOV sonda (`mvhd`, `tkhd`) při uploadu. Délka a velikost se kontrolují při schválení proti `platform.CheckVideo`. |
