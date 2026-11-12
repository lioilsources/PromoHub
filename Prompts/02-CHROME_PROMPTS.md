# 02-CHROME_PROMPTS.md — vlna 0 přes Claude v Chrome

Prompty pro rozšíření Claude v Chrome. Odklikají za tebe zakládání účtů, developer aplikací a IFTTT appletů z §10 plánu `01-ACCOUNTS_PLAN.md`.

## Jak na to

1. V Chromu buď přihlášený do Googlu, Meta, IFTTT a dalších účtů, které už máš. Claude se přihlašovat nebude.
2. Otevři boční panel Claude a vlož **blok 0**. Pak vlož prompt platformy. Jeden prompt = jedna konverzace.
3. Vyplň proměnné v hlavičce promptu (`HANDLE`, …). Proměnné jsou nahoře v bloku, ať je na telefonu nemusíš hledat v textu.
4. Claude se zastaví u hesla, captchy, 2FA, ověření telefonem a u všeho placeného. To uděláš ty a napíšeš „pokračuj“.
5. Na konci ti Claude vypíše, co máš zapsat. Secrets nikdy nezapisuje do chatu: nechá je na obrazovce a řekne, do které proměnné patří.
   - Secrets → `~/stack/promohub/.env` na JODĚ.
   - Handly a ID → `accounts.yaml` v repu.

Pořadí podle vln hubu:

- hned (vlna 1): 1 Bluesky, 2 Mastodon, 5 YouTube (stačí API klíč), 6 Telegram a Discord;
- vlna 2: 8 ostatní účty, pak 3 IFTTT applety;
- vlna 3: 4 Meta;
- vlna 4: 7 TikTok a OAuth část promptu 5.

Adresa hubu je `https://promo.ol1n.com`. Redirect URI pro OAuth jsou `https://promo.ol1n.com/oauth/<platforma>/callback`.

---

## Blok 0 — pravidla (vlož vždy jako první)

```
Pomáháš mi založit promo účty a developer přístupy pro můj hub promohub. Odpovídej česky a stručně.

Pravidla:
- Nikdy nezadávej hesla, kódy 2FA, čísla karet ani telefonní čísla. Na přihlášení, captchu, ověření telefonem nebo e-mailem se zastav a napiš „Čekám na tebe: <co>“.
- Nic neplať, nevybírej placený tarif ani trial vyžadující kartu. Když krok vyžaduje platbu, zastav se a zeptej se.
- Nic nepublikuj, nikoho nesleduj, nikomu nepiš. Jen nastavení.
- Tokeny, klíče, hesla aplikací a client secrets nepiš do chatu ani do žádného pole jiného webu. Nech je zobrazené na stránce a napiš: „Na obrazovce je <co>, zkopíruj do .env jako <PROMĚNNÁ>“. Počkej, až napíšu „mám“, teprve pak pokračuj nebo zavři dialog (některé secrets se ukážou jen jednou).
- Veřejné údaje (handle, ID účtu, ID kanálu, kód pozvánky, app ID) do chatu napsat můžeš.
- Když je stránka jiná, než popisuju (rozhraní se mění), najdi ekvivalent; když nevíš, zeptej se, nehádej.
- Na konci vypiš shrnutí ve tvaru:
  accounts.yaml: <řádky YAML>
  .env: <JMÉNA proměnných, které mám vyplnit, bez hodnot>
  Co zbývá ručně: <seznam>
```

---

## 1. Bluesky

```
HANDLE=<handle, např. ol1n.bsky.social>

Úkol: Bluesky účet pro hub.
1. Otevři https://bsky.app. Když účet HANDLE neexistuje, otevři registraci a zastav se u e-mailu a hesla (vyplním já).
2. V profilu nastav jméno a bio jen pokud jsou prázdné; zeptej se mě na text.
3. Settings → Privacy and security → App passwords → Add App Password, název „promohub“. Heslo nech na obrazovce → proměnná BLUESKY_MAIN_APP_PASSWORD.
4. Zjisti DID účtu: otevři https://bsky.social/xrpc/com.atproto.identity.resolveHandle?handle=HANDLE a opiš "did".
Výstup do accounts.yaml:
  - platform: bluesky
    handle: HANDLE
    external_id: <did>
    auth_ref: BLUESKY_MAIN
```

## 2. Mastodon

```
HANDLE=<uživatelské jméno bez @>
INSTANCE=mastodon.social

Úkol: Mastodon účet a token pro hub.
1. Otevři https://INSTANCE. Když účet neexistuje, otevři registraci a zastav se u e-mailu a hesla.
2. Preferences → Public profile → Privacy and reach: zapni „Include public posts in search results“ a „Suggest account to others“.
3. Preferences → Development → New application:
   - Application name: promohub
   - Website: https://promo.ol1n.com
   - Redirect URI nech výchozí (urn:ietf:wg:oauth:2.0:oob)
   - Scopes: odškrtni vše a zaškrtni jen read, write:statuses, write:media, write:conversations (pokud chybí, jen read, write:statuses, write:media)
   - Ulož, otevři aplikaci a nech na obrazovce „Your access token“ → proměnná MASTODON_MAIN_TOKEN.
Výstup do accounts.yaml:
  - platform: mastodon
    handle: HANDLE@INSTANCE
    auth_ref: MASTODON_MAIN
    hashtags: [indiedev, gamedev, kidsapps]
```

## 3. IFTTT — publikační applety

Pro účty, kam hub publikuje přes IFTTT: X, Facebook Page, LinkedIn, Pinterest a dočasně Threads. Účty X, LinkedIn a Pinterest musí existovat (prompt 8), FB Page z promptu 4.

```
SLUŽBY=x, facebook, linkedin, pinterest, threads   (smaž ty, které ještě nemám)

Úkol: v IFTTT vytvoř pro každou službu ze SLUŽBY jeden applet. Všechny mají stejný trigger:
  If: Webhooks → „Receive a web request“ (NE „with a JSON payload“), Event Name viz tabulka.
  Then: akce podle tabulky. Pole vyplň přesně přes „Add ingredient“:
    Value1 = text postu, Value2 = odkaz, Value3 = URL obrázku.

| služba | Event Name | Then |
| x | publish_x | X (Twitter) → Post a tweet (s obrázkem: „Post a tweet with image“, Image URL = Value3); Tweet text = Value1 |
| facebook | publish_facebook | Facebook Pages → Create a link post; Link URL = Value2, Message = Value1 |
| linkedin | publish_linkedin | LinkedIn → Share a link; Link URL = Value2, Comment = Value1 |
| pinterest | publish_pinterest | Pinterest → Create pin; Board = zeptej se mě, Image URL = Value3, Source URL = Value2, Description = Value1 |
| threads | publish_threads | Threads → Create a link post (nebo text post); Text = Value1, Link = Value2 |

Postup:
1. https://ifttt.com/create. Když je limit appletů vyčerpaný nebo IFTTT chce upgrade, zastav se a řekni mi, kolik appletů mám a jaký tarif.
2. Při připojení služby (Connect) se zastav u přihlášení do té služby.
3. Applet pojmenuj „promohub: publish_<služba>“ a vypni notifikace „Receive notifications when this Applet runs“.
4. Když akce s daným názvem neexistuje, najdi nejbližší (text post s odkazem) a řekni mi, co jsi zvolil.
5. Nakonec otevři https://ifttt.com/maker_webhooks/settings a řekni, jestli je Webhooks key nastavený (hodnotu nepiš; patří do IFTTT_KEY, ten už mám).
Výstup: seznam „služba → event → zvolená akce“ a do accounts.yaml pro každou službu
  - platform: <služba>
    handle: <můj handle na té službě>
    publish: ifttt
    ifttt_event: publish_<služba>
```

## 4. Meta — Instagram, Facebook Page, Threads a Meta aplikace

```
IG_HANDLE=<instagram handle>
FB_PAGE_NAME=<název stránky, např. Ol1n Apps>

Úkol A — účty:
1. Instagram (https://www.instagram.com/accounts/edit/ nebo appka): přepni IG_HANDLE na professional účet (Creator nebo Business, kategorie „App page“ / „Video creator“). Když to web neumí, řekni mi, ať to udělám v telefonu, a pokračuj.
2. Facebook: když stránka FB_PAGE_NAME neexistuje, vytvoř ji (https://www.facebook.com/pages/create), kategorie „App page“. Opiš Page ID (O stránce → Průhlednost stránky, nebo URL).
3. Threads: ověř, že https://www.threads.net/@IG_HANDLE existuje. Když ne, řekni mi, ať profil založím v appce Threads.

Úkol B — Meta aplikace (https://developers.facebook.com/apps):
1. Create app → název „promohub“, use cases zaškrtni: „Access the Threads API“ a „Manage messaging & content on Instagram“ (Instagram API with Instagram Login). Když wizard povolí jen jeden use case, založ dvě aplikace: „promohub-threads“ a „promohub-ig“. Business portfolio nepřipojuj, pokud to není povinné.
2. Threads use case → Settings:
   - Redirect Callback URLs: https://promo.ol1n.com/oauth/threads/callback
   - Uninstall a Delete callback: https://promo.ol1n.com/oauth/threads/deauthorize
   - Permissions přidej: threads_basic, threads_content_publish, threads_manage_insights, threads_manage_replies, threads_read_replies, threads_manage_mentions, threads_keyword_search
   - App roles → Roles → Add people → Threads Tester → IG_HANDLE. Pak otevři https://www.threads.net/settings/account → Website permissions → Invites a pozvánku přijmi.
   - Opiš Threads app ID; Threads app secret nech na obrazovce → THREADS_APP_ID / THREADS_APP_SECRET.
3. Instagram use case → API setup with Instagram login:
   - Opiš Instagram app ID; Instagram app secret nech na obrazovce → INSTAGRAM_APP_ID / INSTAGRAM_APP_SECRET.
   - Business login settings → OAuth redirect URIs: https://promo.ol1n.com/oauth/instagram/callback; Deauthorize a Data deletion: https://promo.ol1n.com/oauth/instagram/deauthorize
   - App roles → Instagram Testers → přidej IG_HANDLE. Pozvánku přijmi na https://www.instagram.com/accounts/manage_access/ → Tester invites.
   - Webhooks nenastavuj.
4. Facebook Login for Business (stejná aplikace nebo „promohub-ig“): přidej produkt Facebook Login, Valid OAuth Redirect URIs: https://promo.ol1n.com/oauth/facebook/callback. Permissions pages_show_list, pages_read_engagement, pages_read_user_content, read_insights. Opiš App ID; App secret (App settings → Basic) nech na obrazovce → FACEBOOK_APP_ID / FACEBOOK_APP_SECRET.
5. App settings → Basic: Privacy Policy URL https://olin.now/privacy (když neexistuje, zeptej se mě), App domains promo.ol1n.com. Aplikaci NEPŘEPÍNEJ do Live a nežádej o App Review.
Výstup do accounts.yaml:
  - platform: instagram
    handle: IG_HANDLE
    auth_ref: oauth
  - platform: threads
    handle: IG_HANDLE
    auth_ref: oauth
  - platform: facebook
    handle: <page username nebo ID>
    external_id: <Page ID>
    auth_ref: oauth
    publish: ifttt
    ifttt_event: publish_facebook
```

## 5. YouTube a Google Cloud

```
CHANNEL=<@handle kanálu>

Úkol: přístup k YouTube Data API a Analytics.
1. https://studio.youtube.com: když kanál neexistuje, založ ho (zastav se u výběru Google účtu). Opiš channel ID (Settings → Channel → Advanced settings, začíná UC).
2. https://console.cloud.google.com/projectcreate → projekt „promohub“ (bez billing účtu; když ho Google vyžaduje, zastav se).
3. APIs & Services → Library → zapni „YouTube Data API v3“ a „YouTube Analytics API“.
4. Credentials → Create credentials → API key. Edit → API restrictions: Restrict key → jen YouTube Data API v3. Klíč nech na obrazovce → YOUTUBE_API_KEY.
5. OAuth consent screen (Google Auth Platform): External, app name „promohub“, support e-mail můj, Audience → Publish app (stav „In production“, ne „Testing“ — Testing vydává refresh tokeny jen na 7 dní). Ověření aplikace Googlem nežádej; varování „unverified app“ při přihlášení je v pořádku.
   Scopes: .../auth/youtube.readonly, .../auth/yt-analytics.readonly, .../auth/youtube.force-ssl
6. Clients → Create client → Web application „promohub“, Authorized redirect URI: https://promo.ol1n.com/oauth/youtube/callback. Client ID opiš; Client secret nech na obrazovce → GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET.
Výstup do accounts.yaml:
  - platform: youtube
    handle: <handle bez @>
    external_id: <UC… channel ID>
    auth_ref: YOUTUBE
```

## 6. Telegram kanál a Discord

```
CHANNEL_USERNAME=<veřejné @jméno kanálu bez @>
BOT_USERNAME=<@jméno bota hubu bez @>

Úkol A — Telegram (https://web.telegram.org/k/):
1. Když kanál CHANNEL_USERNAME neexistuje: New Channel, veřejný, odkaz t.me/CHANNEL_USERNAME. Popis se mě zeptej.
2. Channel info → Administrators → Add admin → BOT_USERNAME, práva jen „Post messages“ a „Edit messages of others“.
3. Moje Telegram user ID: napiš @userinfobot zprávu /start a opiš „Id“ → patří do TELEGRAM_ADMIN_IDS (není secret).
Úkol B — Discord (https://discord.com/app):
1. Na mém serveru vytvoř textový kanál „announcements“ (typ Announcement, pokud server má Community; jinak obyčejný).
2. Edit channel → Integrations → Webhooks → New Webhook „promohub“ → Copy Webhook URL nech na obrazovce → DISCORD_ANNOUNCE_WEBHOOK.
3. Pozvánka: Invite people → Edit invite link → Expire after: Never, Max uses: No limit. Opiš kód (část za discord.gg/).
Výstup do accounts.yaml:
  - platform: telegram
    handle: CHANNEL_USERNAME
    external_id: "@CHANNEL_USERNAME"
  - platform: discord
    handle: <název serveru>
    external_id: <kód pozvánky>
    auth_ref: DISCORD_ANNOUNCE
```

## 7. TikTok developer

```
HANDLE=<tiktok handle>

Úkol: TikTok developer aplikace pro upload draftů.
1. https://developers.tiktok.com → Log in (zastav se u přihlášení) → Manage apps → Connect an app, název „promohub“, kategorie „Utilities“ nebo „Photo & Video“.
2. Products: přidej Login Kit a Content Posting API. U Content Posting API NEZAPÍNEJ „Direct Post“ (vyžaduje audit); stačí upload (Upload to inbox).
3. Scopes: user.info.basic, user.info.stats, video.list, video.upload.
4. Login Kit → Redirect URI (Web): https://promo.ol1n.com/oauth/tiktok/callback.
5. Přepni na Sandbox → Create sandbox → Target users → přidej HANDLE (zastav se, pokud vyžaduje přihlášení v appce).
6. Client key opiš; Client secret nech na obrazovce → TIKTOK_CLIENT_KEY / TIKTOK_CLIENT_SECRET.
7. Aplikaci NEPOSÍLEJ do review.
Výstup do accounts.yaml:
  - platform: tiktok
    handle: HANDLE
    auth_ref: oauth
    publish: api
```

## 8. Ostatní účty: X, Reddit, LinkedIn, Pinterest, Hacker News, Product Hunt, dev.to

```
HANDLE=<preferovaný handle>

Úkol: zkontroluj a založ účty, kde ještě nejsou. Pro každou službu: otevři ji, zjisti, jestli jsem přihlášený a pod jakým jménem. Když účet nemám, otevři registraci s HANDLE (nebo nejbližší volnou variantou) a zastav se u e-mailu, hesla a ověření.
1. X: https://x.com — profil, bio se mě zeptej.
2. Reddit: https://www.reddit.com — jen ověř účet a napiš jeho stáří a karmu (Reddit maže posty nových účtů).
3. LinkedIn: https://www.linkedin.com — osobní profil stačí.
4. Pinterest: https://www.pinterest.com/business/create/ — business účet; vytvoř nástěnky „SwypeKids“, „Hajánek“, „OrbitronTactics“ a opiš jejich URL.
5. Hacker News: https://news.ycombinator.com — jen uživatelské jméno.
6. Product Hunt: https://www.producthunt.com — účet; pak https://www.producthunt.com/v2/oauth/applications → Add an application „promohub“, redirect https://promo.ol1n.com/oauth/producthunt/callback → „Create Token“ (Developer Token) nech na obrazovce → PRODUCTHUNT_TOKEN.
7. dev.to: https://dev.to — účet; Settings → Extensions → DEV Community API Keys → Generate „promohub“, klíč nech na obrazovce → DEVTO_MAIN_API_KEY.
Výstup do accounts.yaml (jen služby, kde účet je):
  - platform: x
    handle: <handle>
    publish: ifttt
    ifttt_event: publish_x
  - platform: reddit
    handle: <handle>
  - platform: linkedin
    handle: <vanity jméno z URL profilu>
    publish: ifttt
    ifttt_event: publish_linkedin
  - platform: pinterest
    handle: <handle>
    publish: ifttt
    ifttt_event: publish_pinterest
    boards: {swypekids: <board URL>, hajanek: <board URL>, orbitron-tactics: <board URL>}
  - platform: hackernews
    handle: <jméno>
  - platform: producthunt
    handle: <jméno>
    auth_ref: PRODUCTHUNT
  - platform: devto
    handle: <jméno>
    auth_ref: DEVTO_MAIN
```
