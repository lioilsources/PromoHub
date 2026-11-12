# RUNBOOK — promohub, fáze 1

Co musíš udělat ručně v Apify, IFTTT a na JODĚ, aby fungoval tok
**Apify run → promohub → IFTTT → Telegram/Discord**. Kód nic z toho nezakládá sám.

Placeholdery v textu:

- `<APIFY_SECRET>` = hodnota `APIFY_WEBHOOK_SECRET`,
- `<IFTTT_SECRET>` = hodnota `IFTTT_INBOUND_SECRET`.

Oba vygeneruj např. `openssl rand -hex 24`. Musí mít aspoň 24 znaků, jinak hub nenastartuje.

---

## 1. JODA: nasazení

1. DNS a tunel: v Cloudflare Zero Trust → Tunnels → tunel JODA přidej public hostname `promo.ol1n.com` → `http://caddy:80`, stejně jako u `hooks.ol1n.com`.
2. Caddy: vlož `deploy/Caddyfile.snippet` do Caddyfile a pak `docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile`.
   - Ven jdou jen `/healthz` a `/webhooks/*`.
   - `/metrics` zůstává jen v síti `web`.
   - Query `secret` se v access logu nahradí za `REDACTED`.
3. Compose: obsah `deploy/docker-compose.yml` přidej do stacku `~/stack` na JODĚ.
   - Do `~/stack/.env`, tedy ke compose, přidej `PROMOHUB_DB_PASSWORD=…`.
   - `~/stack/promohub/.env` vytvoř podle `.env.example` a dej mu `chmod 600`. V `DATABASE_URL` musí být stejné heslo.
   - Pokud chceš sdílený Postgres místo vlastního, smaž službu `promohub-db` a nasměruj `DATABASE_URL` na sdílenou instanci. Je to otevřená otázka §13/3.
4. Config: vyplň v repu `accounts.yaml` (handly a **ID** Apify tasků, viz §2) a `apps.yaml`. Zkontroluj je přes `make check`.
5. Image a nasazení: `make deploy`. Postaví image pro linux/amd64, pushne ho na ghcr, zkopíruje config a spustí `docker compose up -d promohub`.
   - Migrace proběhnou samy při startu.
   - Bez ghcr: v compose přepni na `build: ../PromoHub`.
6. Ověření: `curl https://promo.ol1n.com/healthz` → `ok`.

Logy jsou JSON na stdout: `docker logs -f promohub`.

## 2. Apify konzole

### 2.1 Účet a token
1. Na <https://console.apify.com> → Settings → **API & Integrations** zkopíruj Personal API token do `APIFY_TOKEN`.
   - Doporučení: vytvoř zvlášť token jen pro hub, ať se dá revokovat.
2. Billing: Free plán ($5/měs.) stačí na start. V **Usage → Limits** nastav měsíční limit, např. $10, ať tě nepříjemně nepřekvapí pay-per-result actors.

### 2.2 Tasky
Pro každý řádek níže: Store → vyber actor → **Create task** → vyplň input → Save.

Mapování v `internal/collectors/apifysrc` je ověřené na skutečných bězích (3. 11. 2026) těchto actorů. Při jiném actoru přidej fixture do `real_test.go`.

| Task (name v `accounts.yaml`) | kind | Actor | Input | Schedule |
|---|---|---|---|---|
| `x-profile` | `x-profile` | `apidojo/tweet-scraper` | `twitterHandles: ["<můj handle>"]`, `sort: "Latest"`, `maxItems: 50` | 2× denně |
| `x-mentions` | `x-mentions` | `apidojo/tweet-scraper` | `searchTerms: ["@<handle> OR SwypeKids OR Hajánek OR OrbitronTactics OR olin.now"]`, `sort: "Latest"`, `maxItems: 100` | 2× denně |
| `reddit-mentions` | `reddit-mentions` | `harshmaur/reddit-scraper` | `searchTerms: ["SwypeKids", "Hajánek", …]`, `searchComments: true`, `searchSort: "new"`, `postedAfter: "1 day"`, `commentedAfter: "1 day"`, `maxPostsCount: 50`, `maxCommentsCount: 50` | 2× denně |
| `tiktok-profile` | `tiktok-profile` | `clockworks/tiktok-scraper` | `profiles: ["<handle>"]`, `resultsPerPage: 30`, `shouldDownloadVideos: false`, `shouldDownloadCovers: false` | 1× denně |

Volitelně můžeš přidat tasky:

- `yt-mentions` (kind `yt-mentions`): actor `streamers/youtube-scraper` s inputem `searchQueries: [...]`, `maxResults: 20`, `maxResultsShorts: 0`, `maxResultStreams: 0`. Vlastní videa se poznají podle `channelUsername`, proto do `accounts.yaml` dej YouTube handle bez `@`.
- `ig-profile` (kind `ig-profile`): actor `apify/instagram-profile-scraper` s inputem `usernames: ["<handle>"]`. Vrací followery a ~12 posledních postů. Kolaborační posty jiných účtů hub přeskočí.

Zkušenosti z ověřovacích běhů:

- `trudax/reddit-scraper` **nepoužívej**. Reddit mu obě requesty zablokoval a run přesto skončil jako SUCCEEDED s prázdným datasetem. Hub by pak tiše nedostával nic.
- Platí to obecně: prázdný úspěšný run neznamená „žádné zmínky“. Po prvních bězích zkontroluj `items` v `apify_runs`.
- `clockworks/tiktok-scraper` odmítne run, když je strop ceny („Max cost per run“) pod $0.50. Skutečná cena je ~$0.004 za video.

U každého tasku:

1. **Task ID** zkopíruj z záložky API, je to řetězec jako `AbCdEf123…`, ne název. Patří do `accounts.yaml` → `apify_tasks[].id`. Webhooky z tasků, které tam nejsou, hub odmítne s 403.
2. V záložce **Settings** nastav rozumný timeout a paměť. Max. položky stejně omezuje `max_items`.
3. Webhook nastav v záložce **Integrations** → Add integration → **HTTP webhook**:
   - Event types: jen **Run succeeded** (`ACTOR.RUN.SUCCEEDED`).
   - URL: `https://promo.ol1n.com/webhooks/apify?secret=<APIFY_SECRET>`
   - Payload template nech výchozí. Hub čte `eventData.actorTaskId` a `eventData.actorRunId`, run si pak stejně znovu načte přes API vlastním tokenem.
   - Tlačítkem **Test** pošli testovací webhook. Hub odpoví 403 „task not whitelisted“ nebo 200/202. Obojí znamená, že secret a síť fungují.
4. **Schedule**: Schedules → Create → přidej task a nastav cron (viz tabulka), timezone Europe/Prague.

### 2.3 Ověření
- Spusť task ručně (Start). Po doběhnutí uvidíš v `docker logs promohub` řádek `apify run processed` s počty `items` / `new_inbound` / `events`.
- V DB:
  - `SELECT run_id, task_name, state, items, new_items, cost_usd, last_error FROM apify_runs ORDER BY received_at DESC LIMIT 5;`
  - `state = failed` se samo zkouší znovu s backoffem, maximálně 5×.
- Druhé doručení stejného webhooku vrátí `{"status":"duplicate"}` a nic nezapíše.

## 3. IFTTT konzole

### 3.1 Účet a Webhooks key
1. Plán Starter nebo Pro (§3 plánu). Polling 5 min potřebují jen triggery IFTTT → hub.
2. Připoj službu **Webhooks** (<https://ifttt.com/maker_webhooks>) → Documentation. „Your key is: …“ zkopíruj do `IFTTT_KEY`.
3. Připoj služby **Telegram**, **Discord**, **Google Sheets**, **Notifications**. Pro triggery v §3.3 navíc **YouTube**, **Bluesky** a **Instagram** (business účet).

### 3.2 Applety hub → IFTTT (spouští je `rules.yaml`)
Hub posílá `POST https://maker.ifttt.com/trigger/<event>/json/with/key/<IFTTT_KEY>` s JSON tělem.

V appletu: **If** Webhooks → *Receive a web request with a JSON payload* → Event Name = `<event>`.

Hodnoty z payloadu čti přes filter code (`JSON.parse(MakerWebhooks.jsonEvent.JsonPayload)`) nebo předej celé `{{JsonPayload}}`. Pole `message` je hotový text pro zprávu, takže nejjednodušší applet ho jen přepošle.

| # | Event | Then | Pozn. |
|---|---|---|---|
| 1 | `promo_mention` | Telegram → Send message (+ **AI Action assist**: „Shrň zmínku do 2 vět, odhadni sentiment, navrhni odpověď“) | payload: `platform, kind, author, text, url, app, rule, message` |
| 2 | `promo_review` | Telegram + Google Sheets → Add row | připraveno pro fázi 2 (collectory recenzí) |
| 3 | `promo_review_urgent` | Telegram + Notifications → Send a notification | |
| 4 | `promo_metric_jump` | Discord → Post a message | payload: `metrics`, `deltas`, `url`/`account`, `message` |
| 5 | `promo_digest` | Email / Telegram | fáze 2 |
| 6 | `publish_x` | X → Post a tweet | fáze 3, hub ho zatím neposílá |

Ověření bez hubu: na stránce Webhooks → Documentation pošli testovací event `promo_mention` s JSON `{"message":"test"}`.

### 3.3 Applety IFTTT → hub (spouštěče)
**Then** Webhooks → *Make a web request*:

- URL: `https://promo.ol1n.com/webhooks/ifttt/<event>?secret=<IFTTT_SECRET>`
- Method: `POST`
- Content type: `application/json`
- Body podle tabulky.

| # | If | `<event>` | Body |
|---|---|---|---|
| 7 | YouTube → New public video uploaded by you | `youtube_new_video` | `{"url":"{{Url}}","title":"{{Title}}","body":"{{Description}}","published_at":"{{PublishedAt}}"}` |
| 8 | Bluesky → New post by you | `bluesky_new_post` | `{"url":"{{PostUrl}}","body":"{{Text}}","published_at":"{{CreatedAt}}"}` |
| 9 | Instagram → New photo/video by you | `instagram_new_post` | `{"url":"{{Url}}","body":"{{Caption}}","published_at":"{{CreatedAt}}"}` |
| 12 | AI Agent / cokoliv → spustit sběr hned | `run_task` | `{"task":"x-mentions"}` (jen jména z `accounts.yaml`) |

Názvy ingrediencí (`{{Url}}` atd.) se liší podle služby. Vyber je v editoru tlačítkem **Add ingredient**.

Co hub s triggery dělá:

- Hub založí `content_items` a jen při prvním výskytu vyhodnotí pravidla `on: content.new`. Opakované volání vrátí `{"status":"duplicate"}`.
- `run_task` spustí whitelisted Apify task s jeho uloženým inputem. Výsledek pak přijde zpět standardním Apify webhookem.
- IFTTT requesty nejsou podepsané, takže hub z nich nikdy nebere vstup pro scraper ani pokyn k publikaci.
- Ostatní eventy (`scan`, RSS…) se zatím jen zalogují do tabulky `events` (`type = ifttt.<event>`). Obsluha přijde ve fázi 2.

### 3.4 Smoke test „bez hubu“ (§8 plánu, volitelné)
*If* Apify → Task run finished → *Query* Apify → Get dataset items → *AI Agent* „Z položek vyber komentáře mladší než 24 h…“ → Telegram.

Applet slouží jen k ověření kvality AI Agenta a spotřeby kreditů. Po zprovoznění hubu ho vypni, jinak budou notifikace chodit dvakrát.

## 4. End-to-end ověření fáze 1
1. V Apify ručně spusť `x-mentions`.
2. V logu promohub uvidíš `apify run processed … events=N`.
3. Do Telegramu dorazí zpráva z appletu `promo_mention`.
4. Znovu pošli stejný webhook (Apify → run → Webhooks → Resend) → `duplicate`, žádná nová zpráva.
5. Druhý běh tasku se stejnými tweety → `new_inbound=0`, žádná zpráva. Duplicity jsou nulové.

Stav outboxu:

```sql
SELECT id, type, rule, target, attempts, last_error, sent_at FROM events
WHERE target IS NOT NULL ORDER BY id DESC LIMIT 20;
```

Nedoručené eventy se zkouší znovu s backoffem 30 s → ~1 h, maximálně 8×. 4xx odpověď (např. špatný IFTTT key) znamená okamžitý konec bez dalších pokusů, chyba zůstane v `last_error`. Po opravě je pošleš znovu:

```sql
UPDATE events SET attempts = 0, next_attempt_at = now() WHERE sent_at IS NULL AND target IS NOT NULL;
```

Útrata Apify: `SELECT sum(cost_usd) FROM apify_runs WHERE received_at > now() - interval '7 days';`

## 5. Ještě otevřené (§13 plánu)
Fázi 1 neblokují, ale ovlivní další fáze:

- Instagram business účet,
- subreddity pro posting,
- sdílený vs. vlastní Postgres (compose teď má vlastní),
- model na Sparku,
- Discord přes IFTTT vs. přímo (teď funguje obojí: `ifttt:` a `discord:` sink),
- jazyk postů.

---

## 6. Vlna 1 — čtení přes oficiální API

Hub teď sám čte Bluesky, Mastodon, YouTube, Telegram kanál a Discord. Apify ani IFTTT k tomu nepotřebuje. Plánovač běží uvnitř `serve` a stav ukládá do tabulky `collector_state`.

1. Účty založ podle `Prompts/02-CHROME_PROMPTS.md`: prompty 1, 2, 5 (jen API klíč) a 6.
2. Do `accounts.yaml` doplň handly a odkomentuj nové účty. Pak `make check`.
3. Do `~/stack/promohub/.env` doplň secrets podle `.env.example`: `BLUESKY_MAIN_APP_PASSWORD`, `MASTODON_MAIN_TOKEN`, `YOUTUBE_API_KEY` a pro kanál `TELEGRAM_BOT_TOKEN`.
4. Před nasazením ověř adaptér lokálně proti živému API (bez DB):
   ```
   set -a && . ./.env && set +a && ./bin/promohub probe bluesky:<handle>
   ```
   Vypíše namapované snapshoty a zmínky jako JSON. `PROBE_RAW=1` přidá surové odpovědi na stderr; hodí se pro nové fixtures (zkrátit, bez tokenů).
5. Nasaď: `make deploy`. Migrace `000002` proběhne sama.
6. Stav účtů: `docker exec promohub /promohub accounts` (nebo lokálně s `DATABASE_URL`).
   - `ready` — capability je připravená;
   - `… is not set` — chybí secret. Hub to zkouší znovu jednou za 6 h a nepočítá to jako chybu;
   - `FAILS` — počet selhání po sobě. Při třetím vznikne trigger `account.error`. Pravidlo v `rules.yaml` je zakomentované; zapni ho, až bude sink `telegram:urgent`.

Intervaly (z `internal/platform`):

| Capability | Interval | Platformy |
|---|---|---|
| profil | 3 h | Bluesky, Mastodon, YouTube |
| profil | 6 h | Telegram, Discord |
| zmínky | 10 min | Bluesky, Mastodon |
| zmínky | 15 min | YouTube komentáře |
| hledání | 30 min | Bluesky search, Mastodon hashtagy |

Chyba se opakuje s backoffem do 6 h, 429 respektuje `Retry-After`.

Ruční přeplánování všeho hned:

```sql
UPDATE collector_state SET next_run_at = now();
```
