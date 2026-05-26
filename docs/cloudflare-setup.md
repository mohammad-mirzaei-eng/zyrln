# Cloudflare Worker Setup

An alternative to running your own VPS. The free tier handles personal use easily.

## Deploy the Worker

1. Go to [dash.cloudflare.com](https://dash.cloudflare.com)
2. **Workers & Pages → Create application → Worker**
3. Replace the default code with the contents of [`relay/deploy/cloudflare/worker.js`](../relay/deploy/cloudflare/worker.js)
4. Click **Deploy**
5. Copy the Worker URL:
   `https://your-worker.your-subdomain.workers.dev`

## Update Apps Script

In `relay/deploy/apps-script/Code.gs`, set `EXIT_RELAY_URL` to your Worker URL:

```js
const EXIT_RELAY_URL = "https://your-worker.your-subdomain.workers.dev/relay";
const EXIT_RELAY_KEY = "";   // leave empty — Workers don't use this
```

Then redeploy the Apps Script: **Deploy → Manage deployments → New version**.

## Cloudflare vs VPS

| | Cloudflare Worker | VPS |
|---|---|---|
| Cost | Free | ~$5/mo |
| Setup time | 2 minutes | 15 minutes |
| Fixed IP | No | Yes |
| ChatGPT / Cloudflare-protected sites | No | Yes |
| All other sites | Yes | Yes |
| Passes CAPTCHAs | No | Yes |
| Cloudflare sees traffic metadata | Yes | No |

## Free Tier Limits

- 100,000 requests/day
- 10ms CPU time per request

Enough for normal browsing. Heavy use (video, large downloads) may hit the daily limit — add a second Worker under a different Cloudflare account.
