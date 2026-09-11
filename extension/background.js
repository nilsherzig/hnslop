const API_BASE_URL = "https://hnslop.nilsherzig.com";
const REQUEST_TIMEOUT_MS = 10_000;
const browserAPI = globalThis.browser;

browserAPI.runtime.onMessage.addListener((message) => {
    if (!message || message.type !== "fetch-json") {
        return undefined;
    }

    return fetchJSON(message.path);
});

async function fetchJSON(path) {
    if (typeof path !== "string" || !/^\/v1\/posts\/\d+$/.test(path)) {
        throw new Error("Invalid hnslop API path");
    }

    const url = `${API_BASE_URL}${path}`;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);

    try {
        const response = await fetch(url, {
            method: "GET",
            signal: controller.signal,
        });
        if (!response.ok) {
            throw new Error(`API request failed with HTTP ${response.status}`);
        }

        const payload = await response.json();
        if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
            throw new Error("API returned an invalid JSON object");
        }
        return payload;
    } catch (error) {
        if (error && error.name === "AbortError") {
            throw new Error("API request timed out");
        }
        if (error instanceof Error) {
            throw error;
        }
        throw new Error("API request failed");
    } finally {
        clearTimeout(timeout);
    }
}
