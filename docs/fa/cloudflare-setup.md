# راه‌اندازی Cloudflare Worker

جایگزین VPS — طرح رایگان برای استفاده شخصی کافی است.

## دیپلوی Worker

1. به [dash.cloudflare.com](https://dash.cloudflare.com) برو
2. **Workers & Pages → Create application → Worker**
3. کد پیش‌فرض را پاک کن و محتوای [`relay/deploy/cloudflare/worker.js`](../../relay/deploy/cloudflare/worker.js) را جایگذاری کن
4. روی **Deploy** کلیک کن
5. آدرس Worker را کپی کن:
   `https://worker-name.subdomain.workers.dev`

## به‌روزرسانی Apps Script

در `relay/deploy/apps-script/Code.gs` مقدار `EXIT_RELAY_URL` را به آدرس Worker تنظیم کن:

<div dir="ltr" align="left" style="direction: ltr; text-align: left;">

```js
const EXIT_RELAY_URL = "https://worker-name.subdomain.workers.dev/relay";
const EXIT_RELAY_KEY = "";   // خالی بگذار — Worker به این کلید نیاز ندارد
```

</div>

سپس Apps Script را دوباره دیپلوی کن: **Deploy → Manage deployments → New version**.

## مقایسه Cloudflare و VPS

| | Cloudflare Worker | VPS |
|---|---|---|
| هزینه | رایگان | ~۵ دلار در ماه |
| زمان راه‌اندازی | ۲ دقیقه | ۱۵ دقیقه |
| IP ثابت | ندارد | دارد |
| ChatGPT / سایت‌های Cloudflare | خیر | بله |
| سایر سایت‌ها | بله | بله |
| عبور از CAPTCHA | خیر | بله |
| Cloudflare متادیتای ترافیک را می‌بیند | بله | خیر |

## محدودیت طرح رایگان

- ۱۰۰,۰۰۰ درخواست در روز
- ۱۰ میلی‌ثانیه CPU به ازای هر درخواست

برای مرور عادی کافی است. استفاده سنگین (ویدیو، دانلودهای حجیم) ممکن است به سقف روزانه بخورد — یک Worker دیگر زیر اکانت Cloudflare دیگری اضافه کن.
