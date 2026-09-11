# hnslop

[hnslop](https://hnslop.nilsherzig.com) turns [Salah Adawi's Hacker News AI Detector](https://www.salahadawi.com/hacker-news-ai-detector) into a cached JSON API. Allowing you to programmatically get pangram checks for every hackernews post that reached the frontpage. Feel free to use the hosted instance at [https://hnslop.nilsherzig.com](https://hnslop.nilsherzig.com). Big thanks to Salah (i assume that he had to sell multiple internal organs to pay for his pangram usage).

Please keep in mind that Salah is (as of the time of writing) using Pangram v3.3, which isnt the most up to date model from pangram.

Request one or more posts: 

```bash
curl 'https://hnslop.nilsherzig.com/v1/posts?ids=49582582,49541888' | jq
# {
#   "posts": [
#     {
#       "id": 49582582,
#       "detector": {
#         "ai_score": 33,
#         "url": "https://www.salahadawi.com/hacker-news-ai-detector/49582582"
#       },
#       "cache_status": "hit"
#     },
#     {
#       "id": 49541888,
#       "detector": {
#         "ai_score": 99,
#         "url": "https://www.salahadawi.com/hacker-news-ai-detector/49541888"
#       },
#       "cache_status": "hit"
#     }
#   ]
# }

curl 'https://hnslop.nilsherzig.com/v1/posts/49582582' | jq
# {
#   "id": 49582582,
#   "detector": {
#     "ai_score": 33,
#     "url": "https://www.salahadawi.com/hacker-news-ai-detector/49582582"
#   },
#   "cache_status": "hit"
# }
```

Example client (userscript) at [./userscript.js]:

![userscript client demo](assets/hnslop.png)

## Firefox extension

Download the Firefox extension from [hnslop.nilsherzig.com/extension.xpi](https://hnslop.nilsherzig.com/extension.xpi). The extension source is in [./extension](./extension).

The package served by the Go server is `hnslop.xpi`. Rebuild it with `just extension-rebuild`; for a release build, set `WEB_EXT_API_KEY` and `WEB_EXT_API_SECRET` and run `just extension-sign`. The signing recipe uses Mozilla Add-ons (AMO) to create an unlisted signed extension. Rebuild or sign the package before rebuilding the server or Docker image.
