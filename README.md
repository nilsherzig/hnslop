# hnslop

Turns [Salah Adawi's Hacker News AI Detector](https://www.salahadawi.com/hacker-news-ai-detector) into a cached JSON API. Big thanks to Salah (i assume that he had to sell multiple internal organs to pay for this pangram usage).

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

Example userscript client at [./userscript.js]:

![userscript client demo](assets/hnslop.png)
