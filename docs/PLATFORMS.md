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
