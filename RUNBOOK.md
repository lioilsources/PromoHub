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
6. Stav účtů: `docker exec promohub promohub accounts` (nebo lokálně s `DATABASE_URL`).
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

---

## 7. Vlna 2 — publikace se schválením v Telegramu

Nic se nepublikuje bez tvého ✅ v Telegramu. Bez `TELEGRAM_ADMIN_IDS` je publikace vypnutá úplně.

### Nastavení

1. **Vlastní bot.** U @BotFather založ nového bota jen pro promohub. Hub čte zprávy přes `getUpdates` a druhý proces se stejným tokenem by mu je bral.
   - Token dej do `TELEGRAM_BOT_TOKEN`.
   - Do `TELEGRAM_ADMIN_IDS` dej svoje user ID: pošli `/start` botovi @userinfobot.
   - Botovi pošli `/start`, ať ti smí psát.
2. **Telegram kanál** (volitelný): bota přidej jako admina kanálu (prompt 6 v `Prompts/02-CHROME_PROMPTS.md`). Do `accounts.yaml` přidej `platform: telegram`.
3. **Discord**: `DISCORD_ANNOUNCE_WEBHOOK` a účet `platform: discord` s `auth_ref: DISCORD_ANNOUNCE`.
4. **IFTTT applety** `publish_x`, `publish_facebook`, `publish_linkedin`, `publish_pinterest`, `publish_threads` podle promptu 3.
   - Trigger je „Receive a web request“, pole Value1 = text, Value2 = odkaz, Value3 = obrázek.
   - Bez `IFTTT_KEY` dostanou tyhle účty ruční kartu.
5. **Mastodon token** musí mít `write:statuses` a `write:media`.
6. **Caddy**: nový `deploy/Caddyfile.snippet` zveřejňuje `/media/*` a `/oauth/*` a v logu maže tokeny médií. Potřebuje Caddy ≥ 2.8; `caddy reload` konfiguraci ověří a chybnou odmítne.
7. **Compose**: přibyl volume `promohub-media` (`/data/media`).
8. **LLM** (volitelné): `LLM_BASE_URL` (OpenAI-kompatibilní, …/v1), `LLM_MODEL`, `LLM_API_KEY`. Styl se bere z `voice.md`. Bez nich hub texty jen ořízne na limity, přidá odkaz a hashtagy.
   - Jak hub dostane kapacitu na SPARKu, je otevřená otázka §13/8 plánu.

### Použití

Kampaň vytvoříš zprávou botovi:

```
/post swypekids SwypeKids 2.0 je venku
Offline režim a nová hra. https://olin.now/swypekids
```

- **Syntaxe**: `/post [slug appky] [cs|en] [media=ID] text… [odkaz]`. Poslední URL v textu je odkaz kampaně; je-li text víceřádkový, první řádek je titulek (Reddit, HN, YouTube).
- **Médium**: fotku pošli s popiskem `/post …`. Soubor nad 20 MB nahraj přes `make media FILE=clip.mp4` a do `/post` přidej `media=ID`.
- **Komu draft vznikne**: každému účtu s `publish` ≠ `none`, jehož `apps` a `lang` sedí. Účet bez funkční cesty (chybí publisher nebo `IFTTT_KEY`) dostane ruční kartu s poznámkou proč.
- **Tlačítka na kartě**: ✅ schválit · ✏️ upravit (odpověz novým textem) · 🕒 naplánovat (`18:30`, `zítra 9:00`, `2026-11-20 10:00`, `+2h`, vše pražský čas) · ⏭ přeskočit. Na hlavičce kampaně je „Schválit vše“.
- **Kontroly při schválení**: délka v jednotkách platformy (Bluesky grafémy, X vážené znaky), `max_per_day` účtu a u Pinterestu obrázek.

Co se pak stane podle cesty:

| Cesta | Výsledek |
|---|---|
| `api` | Post vyjde a karta ukáže URL. |
| `ifttt` | Stav `sent`. Hub post spáruje podle textu, až ho najde collector nebo Apify (do 48 h). U účtů s Apify profilem pustí task za 10 min. |
| `manual` | Přijde karta s textem ke zkopírování a tlačítkem pro otevření editoru platformy. Po zveřejnění dej „Vyšlo, pošlu URL“ a odpověz URL; hub začne post sledovat. |

Nové video na YouTube (IFTTT `youtube_new_video` nebo collector, video mladší 48 h) samo navrhne kampaň na ostatní účty.

### Když se něco pokazí

- **Hub spadl během publikace**: draft přejde do `unknown` a bot se zeptá „Vyšlo to?“. Hub ho nikdy sám neopakuje, protože by mohl vzniknout dvojí post.
  - Výjimka je Mastodon: tam je bezpečný retry se stejným `Idempotency-Key`, až 3×.
- **Odmítnuto platformou** (4xx, chybné médium, špatné heslo): `failed`. Oprav příčinu a schval znovu.

Stav draftů:

```sql
SELECT d.id, a.platform, a.handle, d.via, d.status, d.error, d.published_url
FROM drafts d JOIN accounts a ON a.id = d.account_id ORDER BY d.id DESC LIMIT 20;
```

---

## 8. Vlna 3 — OAuth: Instagram, Threads, Facebook Page

1. `PROMOHUB_SECRET_KEY=$(openssl rand -hex 32)` do `.env`. Ztráta klíče znamená nový `/connect` u všech OAuth účtů.
2. Meta aplikace podle promptu 4 v `Prompts/02-CHROME_PROMPTS.md`.
   - Do `.env` patří `INSTAGRAM_APP_ID/SECRET`, `THREADS_APP_ID/SECRET` a `FACEBOOK_APP_ID/SECRET`.
   - Redirect URI jsou `https://promo.ol1n.com/oauth/{instagram|threads|facebook}/callback`.
3. Odkomentuj účty v `accounts.yaml`. U Facebooku `external_id` = Page ID. Pak `make deploy`.
4. V Telegramu napiš botovi `/connect instagram:<handle>`, klikni na „Připojit“ a povol přístup. To samé zopakuj pro Threads a Facebook. Odkaz platí 10 minut.
   - Bez bota: `docker exec promohub promohub connect instagram:<handle>` vypíše odkaz.
5. `promohub accounts` u nich ukáže `ready`. Collectory se pustí hned po připojení.
6. Apify task `ig-profile` můžeš smazat (nebo u účtu nastavit `read: apify`, pokud chceš zůstat u scraperu).

Tokeny Instagramu a Threads platí 60 dní. Hub je obnovuje sám, když zbývá míň než 20 dní. Pokud obnova selže nebo zbývá míň než 7 dní, přijde jednou denně zpráva 🔐 a trigger `account.auth_expiring`. Pak stačí znovu `/connect`.

Publikace:

- Instagram potřebuje obrázek nebo video. Médium bere z veřejné `/media/{token}` URL, takže musí fungovat Caddy snippet z vlny 2.
- Threads publikuje i čistý text a odkaz dá jako link attachment.
- Facebook zůstává na IFTTT, dokud Meta aplikace není Live.

---

## 9. Vlna 4 — video, TikTok a YouTube

Pošli video botovi s popiskem `/post …`, do 20 MB přímo. Větší nahraj přes `make media FILE=clip.mp4` a pak použij `/post … media=ID`.

- Hub bere jen MP4/MOV a nic nepřekódovává. Vertikální 9:16 do 3 min projde všude: Reels, Shorts, TikTok, Bluesky.
- Délka a velikost se kontrolují až při schválení. Pokud video pro platformu nesedí, karta to řekne a draft zůstane neschválený.

| Platforma | Co se stane s videem |
|---|---|
| Bluesky, Mastodon, Telegram kanál | Rovnou vyjde. |
| Instagram (Reels), Threads | Vyjde přes kontejner. |
| TikTok | `publish: api` + `/connect tiktok:<handle>` (prompt 7). Video přijde jako draft do TikTok inboxu a bot pošle popisek. V appce ho dokončíš a pak dáš „Vyšlo, pošlu URL“. Bez OAuth zůstává ruční karta. |
| YouTube Shorts | Ruční karta s titulkem a popisem. Po schválení auditu Google API přepni účet na `publish: api` a `/connect youtube:<handle>`. |

YouTube Analytics: po `/connect youtube:<handle>` (OAuth klient z promptu 5) přibudou u videí metriky za 28 dní: `watch_minutes_28d`, `avg_view_seconds_28d` a `subscribers_gained_28d`.

TikTok čtení přes API místo Apify: u účtu nastav `read: api` a Apify task `tiktok-profile` smaž.

---

## 10. Vlna 5 — HN, Product Hunt, dev.to, odpovědi a /stats

1. Účty podle promptu 8 v `Prompts/02-CHROME_PROMPTS.md`. Do `accounts.yaml`:
   - `platform: hackernews` (handle = HN jméno);
   - `platform: producthunt` s `auth_ref: PRODUCTHUNT` → `PRODUCTHUNT_TOKEN`;
   - `platform: devto` s `auth_ref: DEVTO_MAIN` → `DEVTO_MAIN_API_KEY`.
2. Zmínky appek na HN hledá hub každých 30 min podle jmen a klíčových slov z `apps.yaml`.
3. **Odpovědi z Telegramu.** Pravidlo s cílem `telegram:<sink>` pro `inbound.*` pošle zprávu přes bota s tlačítkem ↩️ Odpovědět. Odpověz textem a hub ho pošle na platformu pod tvým účtem, bez dalšího schvalování.
   - Funguje na Bluesky, Mastodonu, Threads, Instagramu, Facebooku a YouTube (s OAuth).
   - Sink musí být číselné chat ID, ne `@kanál`.
   - Příklad v `rules.yaml`:
     ```yaml
     - name: mention_tg
       on: inbound.mention
       then: telegram:urgent
     ```
4. **`/stats`** v Telegramu vypíše tabulku účtů: sledující, změna za 7 a 30 dní, posty za 30 dní a průměrný engagement na post.

Nehotové: Apify kind `reddit-profile`, protože chybí fixture z reálného běhu actoru. Reddit profil zatím nemá metriky, zmínky jdou dál přes `reddit-mentions`.

---

## 11. Noční LLM okno na SPARKu

Dohodnuto s Directorem (správce spark-broker). Hub drží frontu kampaní sám a LLM si od brokeru půjčí, jen když je co dělat.

- `/post …` kampaň jen uloží a odpoví „⏳ počká na noční LLM okno“. Totéž platí pro návrh z nového YouTube videa.
- Ve **00:15** (pražský čas, `LLM_WINDOW`) se hub podívá do fronty:
  - **Fronta je prázdná:** žádný lease, GPU se nepřepíná.
  - **Něco čeká:**
    1. Hub si přes broker vezme lease (projekt `promohub`, priorita 15, služba `qwen36`, 20 min) a heartbeatuje každé 2 min.
    2. Kampaně přepíše přes LiteLLM gateway (`openclaw-default`).
    3. Pošle karty ke schválení do Telegramu.
    4. Hned dá `done` s metrikou `campaigns ok/failed`, ta jde do souhrnu brokeru.
- **SPARK do 60 min nedá lease** (třeba běží tvoje okno ComfyUI): hub lease stáhne, napíše to do Telegramu a kampaně počkají na další noc.
- **Model neodpovídá** (500/503 nebo spojení selže, 3× po 30 s): hub dá `fail`, kampaně počkají a přijde zpráva. `/health` gateway je zelený i bez modelu, proto rozhoduje skutečné volání.
- **`/post now …`** obejde frontu a drafty pošle hned, bez LLM.

Nastavení v `~/stack/promohub/.env`:

| Proměnná | Hodnota |
|---|---|
| `LLM_WINDOW` | `00:15` |
| `SPARK_BROKER` | `http://<SPARK_HOST>:8100` (adresa z privátního infra.md, ne do repa) |
| `SPARK_BROKER_TOKEN` | prázdné, broker token zatím nechce |
| `LLM_BASE_URL` | `http://<SPARK_HOST>:8080/v1` |
| `LLM_MODEL` | `openclaw-default` |
| `LLM_API_KEY` | prázdné |

Bez `LLM_WINDOW` hub volá LLM rovnou při `/post`; to má smysl jen s trvale běžícím modelem.
