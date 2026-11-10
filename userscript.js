// ==UserScript==
// @name         hnslop – Pangram scores on Hacker News
// @namespace    hnslop
// @version      0.6.1
// @description  Show Pangram AI-detector scores next to Hacker News stories on news.ycombinator.com and hcker.news.
// @match        https://news.ycombinator.com/*
// @match        https://www.news.ycombinator.com/*
// @match        https://hcker.news/*
// @match        https://www.hcker.news/*
// @grant        GM_getValue
// @grant        GM_setValue
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
    const FILTER_CONTROL_ID = "hnslop-filter-control";
    const FILTER_INPUT_ID = "hnslop-filter-input";
    const MAX_AI_SCORE_STORAGE_KEY = "maxAiScorePercent";
    const LOG_PREFIX = "[hnslop]";
    const HIDDEN_CLASS = "hnslop-hidden";
    const RESCAN_DELAY_MS = 100;
    const POST_REQUEST_CONCURRENCY = 6;
    const postRequests = new Map();
    const postResults = new Map();
    let maxAiScorePercent = null;

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

    #${FILTER_CONTROL_ID} {
      display: inline-block;
      margin-left: 0.6em;
      white-space: nowrap;
    }

    #${FILTER_INPUT_ID} {
      box-sizing: border-box;
      width: 4em;
      margin-left: 0.25em;
      padding: 1px 2px;
      border: 1px solid #828282;
      border-radius: 2px;
      background: #fff;
      color: #000;
      font: inherit;
      line-height: inherit;
      vertical-align: baseline;
    }

    #${FILTER_INPUT_ID}:invalid {
      border-color: #b33a3a;
    }

    /* Match hcker.news' muted controls; its theme variables follow the
       selected theme, the fallbacks keep the control readable without them. */
    #${FILTER_CONTROL_ID}[data-site="hcker.news"] {
      display: inline-flex;
      align-items: center;
      gap: 0.4em;
      margin-left: 0.75em;
      color: var(--ink-soft, #828282);
      font-family: Verdana, Geneva, sans-serif;
      font-size: var(--font-size-smaller-val, 12px);
      font-weight: 500;
    }

    #${FILTER_CONTROL_ID}[data-site="hcker.news"] #${FILTER_INPUT_ID} {
      width: 3.5em;
      margin: 0;
      padding: 1px 4px;
      border: 1px solid var(--line-mid, #828282);
      border-radius: 4px;
      background: transparent;
      color: var(--ink, inherit);
      font: inherit;
      appearance: textfield;
      -moz-appearance: textfield;
    }

    #${FILTER_CONTROL_ID}[data-site="hcker.news"] #${FILTER_INPUT_ID}::-webkit-inner-spin-button,
    #${FILTER_CONTROL_ID}[data-site="hcker.news"] #${FILTER_INPUT_ID}::-webkit-outer-spin-button {
      appearance: none;
      margin: 0;
    }

    #${FILTER_CONTROL_ID}[data-site="hcker.news"] #${FILTER_INPUT_ID}:focus {
      border-color: var(--accent, #ff6600);
      outline: none;
    }

    #${FILTER_CONTROL_ID}[data-site="hcker.news"] #${FILTER_INPUT_ID}:invalid {
      border-color: var(--status-error-text-color, #b33a3a);
    }

    .${HIDDEN_CLASS} {
      display: none !important;
    }
  `;

    // Each supported site describes where stories live in its DOM. Dynamic
    // sites render stories after load and re-render them later, so the client
    // watches them for new or replaced story elements.
    const SITES = [
        {
            name: "news.ycombinator.com",
            hostnames: new Set(["news.ycombinator.com", "www.news.ycombinator.com"]),
            dynamic: false,
            storyRows() {
                return [...document.querySelectorAll("tr.athing:not(.comtr)[id]")];
            },
            storyId(row) {
                return row.id;
            },
            filterHost() {
                return document.querySelector(".pagetop");
            },
            scorePlacement(row) {
                const metadataRow = row.nextElementSibling;
                const parent = metadataRow?.querySelector(".subline")
                    || metadataRow?.querySelector(".subtext")
                    || row;
                const commentsLinks = [...parent.querySelectorAll('a[href*="item?id="]')];
                return {
                    parent,
                    reference: commentsLinks[commentsLinks.length - 1] || null,
                };
            },
            setHidden(row, hidden) {
                row.hidden = hidden;
                const metadataRow = row.nextElementSibling;
                if (!metadataRow) {
                    return;
                }
                metadataRow.hidden = hidden;
                const spacerRow = metadataRow.nextElementSibling;
                if (spacerRow && spacerRow.classList.contains("spacer")) {
                    spacerRow.hidden = hidden;
                }
            },
        },
        {
            name: "hcker.news",
            hostnames: new Set(["hcker.news", "www.hcker.news"]),
            dynamic: true,
            storyRows() {
                return [...document.querySelectorAll(".story[data-story-id]")];
            },
            storyId(row) {
                return row.dataset.storyId;
            },
            filterHost() {
                return document.querySelector("#header .header-text-content");
            },
            scorePlacement(row) {
                // Append after hcker.news' trailing overflow menu; placing the
                // score before it leaves a gap where the menu button appears.
                return {
                    parent: row.querySelector(".story-meta") || row,
                    reference: null,
                };
            },
            setHidden(row, hidden) {
                row.classList.toggle(HIDDEN_CLASS, hidden);
            },
        },
    ];

    const site = SITES.find((candidate) => candidate.hostnames.has(location.hostname.toLowerCase())) || null;

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

    function isValidMaxAiScorePercent(value) {
        return typeof value === "number"
            && Number.isInteger(value)
            && value >= 0
            && value <= 100;
    }

    async function restoreMaxAiScore() {
        try {
            const value = await GM_getValue(MAX_AI_SCORE_STORAGE_KEY, null);
            maxAiScorePercent = isValidMaxAiScorePercent(value) ? value : null;
        } catch (error) {
            warn("Could not restore the AI score filter", { error });
        }
    }

    function persistMaxAiScore(value) {
        try {
            void Promise.resolve(GM_setValue(MAX_AI_SCORE_STORAGE_KEY, value)).catch((error) => {
                warn("Could not save the AI score filter", { error });
            });
        } catch (error) {
            warn("Could not save the AI score filter", { error });
        }
    }

    function installFilterControl() {
        if (document.getElementById(FILTER_CONTROL_ID)) {
            log("Filter control already installed");
            return;
        }

        const host = site.filterHost();
        if (!host) {
            warn("Could not find a place for the filter control", { site: site.name });
            return;
        }

        const control = document.createElement("span");
        control.id = FILTER_CONTROL_ID;
        control.dataset.site = site.name;

        const label = document.createElement("label");
        label.htmlFor = FILTER_INPUT_ID;
        label.textContent = "max AI score:";

        const input = document.createElement("input");
        input.id = FILTER_INPUT_ID;
        input.type = "number";
        input.min = "0";
        input.max = "100";
        input.step = "1";
        input.placeholder = "off";
        input.autocomplete = "off";
        input.title = "Hide stories with an AI score above this value; leave empty to show all stories";
        input.setAttribute("aria-label", "Maximum allowed AI score in percent");
        input.value = maxAiScorePercent === null ? "" : String(maxAiScorePercent);
        input.addEventListener("input", () => {
            const value = input.value.trim();
            if (value === "") {
                maxAiScorePercent = null;
                persistMaxAiScore(maxAiScorePercent);
                input.removeAttribute("aria-invalid");
                reapplyFilter();
                return;
            }

            const score = Number(value);
            if (!Number.isInteger(score) || score < 0 || score > 100) {
                input.setAttribute("aria-invalid", "true");
                return;
            }

            maxAiScorePercent = score;
            persistMaxAiScore(maxAiScorePercent);
            input.removeAttribute("aria-invalid");
            reapplyFilter();
        });

        if (site.dynamic) {
            control.append(label, input);
        } else {
            control.append(" | ", label, " ", input);
        }
        host.append(" ", control);
        log("Installed AI score filter control");
    }

    function isItemPage() {
        return site?.name === "news.ycombinator.com" && location.pathname === "/item" && /^\d+$/.test(
            new URLSearchParams(location.search).get("id") || "",
        );
    }

    function storyId(row) {
        return site.storyId(row);
    }

    function storyRows({ quiet = false } = {}) {
        const rows = site.storyRows().filter((row) => /^\d+$/.test(storyId(row) || ""));
        if (!quiet) {
            log("Found story rows", {
                source: site.name,
                count: rows.length,
                ids: rows.map(storyId),
            });
        }
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
                postResults.set(id, post);
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

    function scoreElement(row) {
        const { parent, reference } = site.scorePlacement(row);
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
        return typeof maxAiScorePercent === "number"
            && Number.isFinite(maxAiScorePercent)
            && score !== null
            && score > maxAiScorePercent;
    }

    function applyFilter(row, post) {
        if (shouldHidePost(post)) {
            const score = numericAiScore(post);
            log("Hiding story because it exceeds the AI score filter", {
                id: storyId(row),
                score,
                maximum: maxAiScorePercent,
            });
            site.setHidden(row, true);
            return;
        }

        // Also undo a previous filter decision when the script is reloaded
        // with filtering disabled or with a different threshold.
        site.setHidden(row, false);
    }

    function reapplyFilter() {
        const rows = storyRows();
        let evaluated = 0;
        let hidden = 0;
        rows.forEach((row) => {
            const post = postResults.get(storyId(row));
            if (!post) {
                return;
            }
            evaluated += 1;
            applyFilter(row, post);
            if (shouldHidePost(post)) {
                hidden += 1;
            }
        });
        log("Reapplied AI score filter", {
            maximum: maxAiScorePercent,
            evaluated,
            hidden,
        });
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
        element.dataset.storyId = id;
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
        element.dataset.storyId = storyId(row);
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

    // Dynamic sites can add stories later or replace a rendered story's
    // metadata. Render every story whose score element is missing or belongs
    // to a different story; cached posts make re-renders cheap.
    function renderNewRows() {
        const rows = storyRows({ quiet: true }).filter((row) => {
            const element = scoreElement(row);
            if (element.dataset.storyId === storyId(row)) {
                return false;
            }
            element.dataset.storyId = storyId(row);
            delete element.dataset.verdict;
            element.textContent = " | …";
            return true;
        });
        if (!rows.length) {
            return;
        }
        log("Found new story rows", {
            source: site.name,
            count: rows.length,
            ids: rows.map(storyId),
        });
        void renderRowsIndividually(rows);
    }

    function observeStories() {
        let timer = null;
        const observer = new MutationObserver(() => {
            if (timer !== null) {
                return;
            }
            timer = setTimeout(() => {
                timer = null;
                renderNewRows();
            }, RESCAN_DELAY_MS);
        });
        observer.observe(document.body, { childList: true, subtree: true });
        log("Watching for story changes", { site: site.name });
    }

    async function main() {
        const itemPage = isItemPage();
        log("Userscript started", {
            url: location.href,
            site: site && site.name,
            page: itemPage ? "news-item" : "news-view",
        });

        if (!site) {
            log("Skipping unsupported page");
            return;
        }

        installStyles();
        if (site.dynamic) {
            await restoreMaxAiScore();
            installFilterControl();
            renderNewRows();
            observeStories();
            return;
        }

        const rows = storyRows();
        if (!rows.length) {
            warn("Current Hacker News view did not contain any story rows");
            return;
        }

        await restoreMaxAiScore();
        installFilterControl();
        reserveScoreSpace(rows);
        if (itemPage) {
            await renderItemPage(rows[0]);
        } else {
            await renderHomePage(rows);
        }
    }

    void main().catch((error) => logError("Unexpected userscript error", error));
})();
