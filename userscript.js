// ==UserScript==
// @name         hnslop – Pangram scores on Hacker News
// @namespace    hnslop
// @version      0.5.0
// @description  Show Pangram AI-detector scores next to Hacker News stories.
// @match        https://news.ycombinator.com/*
// @match        https://www.news.ycombinator.com/*
// @grant        GM_xmlhttpRequest
// @connect      hnslop.nilsherzig.com
// @run-at       document-idle
// @noframes
// ==/UserScript==

(() => {
    "use strict";

    // Change this if the hnslop API is running somewhere other than the default
    // public address.
    const API_BASE_URL = "https://hnslop.nilsherzig.com";
    const REQUEST_TIMEOUT_MS = 10_000;
    const SCORE_CLASS = "hnslop-score";
    const LOG_PREFIX = "[hnslop]";
    const NEWS_HOSTNAMES = new Set(["news.ycombinator.com", "www.news.ycombinator.com"]);
    const POST_REQUEST_CONCURRENCY = 6;

    // Set this to a number such as 70 to hide posts with a score strictly above it.
    // Set it to null to disable filtering.
    const MAX_AI_SCORE_PERCENT = null;
    const postRequests = new Map();

    function log(message, details) {
        if (details === undefined) {
            console.info(LOG_PREFIX, message);
        } else {
            console.info(LOG_PREFIX, message, details);
        }
    }

    function warn(message, details) {
        if (details === undefined) {
            console.warn(LOG_PREFIX, message);
        } else {
            console.warn(LOG_PREFIX, message, details);
        }
    }

    function logError(message, details) {
        if (details === undefined) {
            console.error(LOG_PREFIX, message);
        } else {
            console.error(LOG_PREFIX, message, details);
        }
    }

    const styles = `
    .${SCORE_CLASS} {
      display: inline-block;
      margin-left: 0.45em;
      color: #828282 !important;
      font-family: inherit;
      font-size: inherit;
      font-weight: inherit;
      line-height: inherit;
      vertical-align: baseline;
      width: 6ch;
      white-space: nowrap;
    }

    .${SCORE_CLASS} a,
    .${SCORE_CLASS} a:visited {
      color: inherit !important;
      text-decoration: none;
    }

    .${SCORE_CLASS} a:hover {
      text-decoration: underline;
    }

    .${SCORE_CLASS}[data-verdict="human"],
    .${SCORE_CLASS}[data-verdict="human"] a,
    .${SCORE_CLASS}[data-verdict="human"] a:visited {
      color: #16803c !important;
    }

    .${SCORE_CLASS}[data-verdict="mixed"],
    .${SCORE_CLASS}[data-verdict="mixed"] a,
    .${SCORE_CLASS}[data-verdict="mixed"] a:visited {
      color: #a56a00 !important;
    }

    .${SCORE_CLASS}[data-verdict="ai"],
    .${SCORE_CLASS}[data-verdict="ai"] a,
    .${SCORE_CLASS}[data-verdict="ai"] a:visited {
      color: #b33a3a !important;
    }

    .${SCORE_CLASS}[data-verdict="failed"],
    .${SCORE_CLASS}[data-verdict="failed"] a,
    .${SCORE_CLASS}[data-verdict="failed"] a:visited,
    .${SCORE_CLASS}[data-verdict="not-found"],
    .${SCORE_CLASS}[data-verdict="not-found"] a,
    .${SCORE_CLASS}[data-verdict="not-found"] a:visited {
      color: #828282 !important;
    }
  `;

    function installStyles() {
        if (document.getElementById("hnslop-styles")) {
            log("Styles already installed");
            return;
        }
        const style = document.createElement("style");
        style.id = "hnslop-styles";
        style.textContent = styles;
        document.head.append(style);
        log("Installed styles");
    }

    function isNewsPage() {
        return NEWS_HOSTNAMES.has(location.hostname.toLowerCase());
    }

    function isItemPage() {
        return isNewsPage() && location.pathname === "/item" && /^\d+$/.test(
            new URLSearchParams(location.search).get("id") || "",
        );
    }

    function isHomePage() {
        return isNewsPage() && location.pathname === "/";
    }

    function storyId(row) {
        return row.id;
    }

    function storyRows() {
        const rows = [...document.querySelectorAll("tr.athing:not(.comtr)[id]")].filter((row) =>
            /^\d+$/.test(storyId(row)),
        );
        log("Found story rows", {
            source: "news.ycombinator.com",
            count: rows.length,
            ids: rows.map(storyId),
        });
        return rows;
    }

    function requestJson(path) {
        const url = `${API_BASE_URL}${path}`;
        log("Starting API request", { method: "GET", url });

        return new Promise((resolve, reject) => {
            GM_xmlhttpRequest({
                method: "GET",
                url,
                timeout: REQUEST_TIMEOUT_MS,
                responseType: "json",
                onload: (response) => {
                    log("Received API response", {
                        url,
                        status: response.status,
                        cache: responseHeader(response, "x-hnslop-cache"),
                        upstreamStatus: responseHeader(response, "x-hnslop-upstream-status"),
                    });
                    if (response.status < 200 || response.status >= 300) {
                        const error = new Error(`API request failed with HTTP ${response.status}`);
                        warn("API request returned an error status", { url, status: response.status });
                        reject(error);
                        return;
                    }

                    const payload = typeof response.response === "string"
                        ? parseJson(response.response)
                        : response.response || parseJson(response.responseText);
                    if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
                        const error = new Error("API returned an invalid JSON object");
                        warn("API response was not a JSON object", { url });
                        reject(error);
                        return;
                    }

                    resolve(payload);
                },
                onerror: (details) => {
                    const error = new Error("API request failed");
                    logError("API request failed", { url, details });
                    reject(error);
                },
                ontimeout: () => {
                    const error = new Error("API request timed out");
                    logError("API request timed out", { url, timeout: REQUEST_TIMEOUT_MS });
                    reject(error);
                },
            });
        });
    }

    function parseJson(value) {
        try {
            return JSON.parse(value);
        } catch {
            return null;
        }
    }

    function responseHeader(response, name) {
        const wanted = name.toLowerCase();
        const lines = String(response.responseHeaders || "").split(/\r?\n/);
        const line = lines.find((value) => value.toLowerCase().startsWith(`${wanted}:`));
        return line ? line.slice(line.indexOf(":") + 1).trim() : "";
    }

    function requestPost(id) {
        const cached = postRequests.get(id);
        if (cached) {
            log("Reusing post request", { id });
            return cached;
        }

        const request = requestJson(`/v1/posts/${id}`)
            .then((post) => {
                if (Number(post.id) !== Number(id)
                    || !Object.prototype.hasOwnProperty.call(post, "detector")
                    || (post.detector !== null && typeof post.detector !== "object")) {
                    throw new Error("API returned an invalid post object");
                }
                log("Parsed API post object", {
                    id,
                    responseId: post.id,
                    hasDetector: Boolean(post.detector),
                    score: post.detector && post.detector.ai_score,
                    verdict: post.detector && verdictFromScore(numericAiScore(post)),
                });
                return post;
            })
            .catch((error) => {
                postRequests.delete(id);
                throw error;
            });
        postRequests.set(id, request);
        return request;
    }

    function scorePlacement(row) {
        const metadataRow = row.nextElementSibling;
        const parent = metadataRow?.querySelector(".subline")
            || metadataRow?.querySelector(".subtext")
            || row;
        const commentsLinks = [...parent.querySelectorAll('a[href*="item?id="]')];
        return {
            parent,
            reference: commentsLinks[commentsLinks.length - 1] || null,
        };
    }

    function scoreElement(row) {
        const { parent, reference } = scorePlacement(row);
        let element = parent.querySelector(`:scope > .${SCORE_CLASS}`)
            || row.querySelector(`.${SCORE_CLASS}`);
        const created = !element;
        if (created) {
            element = document.createElement("span");
            element.className = SCORE_CLASS;
            element.textContent = " | …";
        }

        const alreadyPlaced = element.parentElement === parent
            && (!reference || element.previousElementSibling === reference);
        if (!alreadyPlaced) {
            if (reference) {
                reference.after(" ", element);
            } else {
                parent.append(" ", element);
            }
            log(created ? "Added score placeholder" : "Moved score placeholder", {
                id: storyId(row),
                placement: reference ? "after-comments" : "story-meta-fallback",
            });
        }
        return element;
    }

    function reserveScoreSpace(rows) {
        rows.forEach(scoreElement);
        log("Reserved score layout space", {
            count: rows.length,
            width: "6ch",
        });
    }

    function numericAiScore(post) {
        const value = post && post.detector && post.detector.ai_score;
        if (value === null || value === undefined) {
            return null;
        }
        const score = Number(value);
        return Number.isFinite(score) ? score : null;
    }

    function verdictFromScore(score) {
        if (score === null) {
            return "failed";
        }
        if (score < 30) {
            return "human";
        }
        if (score <= 70) {
            return "mixed";
        }
        return "ai";
    }

    function shouldHidePost(post) {
        const score = numericAiScore(post);
        return typeof MAX_AI_SCORE_PERCENT === "number"
            && Number.isFinite(MAX_AI_SCORE_PERCENT)
            && score !== null
            && score > MAX_AI_SCORE_PERCENT;
    }

    function hideStory(row) {
        row.hidden = true;
        const metadataRow = row.nextElementSibling;
        if (!metadataRow) {
            return;
        }
        metadataRow.hidden = true;
        const spacerRow = metadataRow.nextElementSibling;
        if (spacerRow && spacerRow.classList.contains("spacer")) {
            spacerRow.hidden = true;
        }
    }

    function showStory(row) {
        row.hidden = false;
        const metadataRow = row.nextElementSibling;
        if (!metadataRow) {
            return;
        }
        metadataRow.hidden = false;
        const spacerRow = metadataRow.nextElementSibling;
        if (spacerRow && spacerRow.classList.contains("spacer")) {
            spacerRow.hidden = false;
        }
    }

    function applyFilter(row, post) {
        if (shouldHidePost(post)) {
            const score = numericAiScore(post);
            log("Hiding story because it exceeds the AI score filter", {
                id: storyId(row),
                score,
                maximum: MAX_AI_SCORE_PERCENT,
            });
            hideStory(row);
            return;
        }

        // Also undo a previous filter decision when the script is reloaded
        // with filtering disabled or with a different threshold.
        showStory(row);
    }

    function renderScore(row, post) {
        const element = scoreElement(row);
        const detector = post && post.detector;
        const scoreValue = numericAiScore(post);
        const verdict = detector ? verdictFromScore(scoreValue) : "not-found";
        const score = scoreValue !== null ? `${scoreValue}%` : "—";
        const label = scoreValue !== null ? `${score} LLM` : score;
        const id = storyId(row);

        log("Rendering score", {
            id,
            score,
            verdict,
            hasDetector: Boolean(detector),
            detectorUrl: detector && detector.url,
        });
        element.dataset.verdict = verdict;
        element.title = detector
            ? `${label} (${verdict})`
            : "No Pangram analysis found for this post";

        if (detector && detector.url) {
            const link = document.createElement("a");
            link.href = detector.url;
            link.target = "_blank";
            link.rel = "noopener noreferrer";
            link.textContent = label;
            element.replaceChildren(" | ", link);
        } else {
            element.textContent = ` | ${label}`;
        }

        applyFilter(row, post);
    }

    function renderError(row, error) {
        warn("Rendering an unavailable score", { id: storyId(row), error: error && error.message });
        const element = scoreElement(row);
        element.dataset.verdict = "failed";
        element.title = "Could not load the Pangram score";
        element.textContent = " | Pangram: ?";
    }

    async function renderRowsIndividually(rows) {
        let nextIndex = 0;
        const worker = async () => {
            while (nextIndex < rows.length) {
                const row = rows[nextIndex++];
                const id = storyId(row);
                try {
                    const post = await requestPost(id);
                    renderScore(row, post);
                } catch (error) {
                    logError("Unable to load story Pangram score", { id, error });
                    renderError(row, error);
                }
            }
        };

        const workerCount = Math.min(POST_REQUEST_CONCURRENCY, rows.length);
        await Promise.all(Array.from({ length: workerCount }, worker));
    }

    async function renderHomePage(rows) {
        log("Loading scores for story view", {
            rowCount: rows.length,
            source: "Salahadawi per-post endpoint",
        });
        await renderRowsIndividually(rows);
        log("Finished rendering story view scores", { rendered: rows.length });
    }

    async function renderItemPage(row) {
        const id = storyId(row);
        log("Loading score for item page", { id });
        try {
            const post = await requestPost(id);
            log("Received item page post", {
                id,
                responseId: post.id,
                hasDetector: Boolean(post.detector),
            });
            renderScore(row, post);
            log("Finished rendering item page score", { id });
        } catch (error) {
            logError("Unable to load item page Pangram score", { id, error });
            renderError(row, error);
        }
    }

    async function main() {
        const newsPage = isNewsPage();
        const itemPage = isItemPage();
        const homePage = isHomePage();
        log("Userscript started", {
            url: location.href,
            page: itemPage ? "news-item" : homePage ? "news-home" : "news-view",
        });

        if (!newsPage) {
            log("Skipping unsupported page");
            return;
        }

        installStyles();
        const rows = storyRows();
        if (!rows.length) {
            warn("Current Hacker News view did not contain any story rows");
            return;
        }

        reserveScoreSpace(rows);
        if (itemPage) {
            await renderItemPage(rows[0]);
        } else {
            await renderHomePage(rows);
        }
    }

    void main().catch((error) => logError("Unexpected userscript error", error));
})();
