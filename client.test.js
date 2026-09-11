const assert = require("node:assert/strict");
const { readFile } = require("node:fs/promises");
const test = require("node:test");
const vm = require("node:vm");

const POST = {
    id: 1,
    detector: {
        ai_score: 90,
        url: "https://example.test/1",
    },
};
const STORAGE_KEY = "maxAiScorePercent";

class FakeElement {
    constructor(document, tagName, className = "") {
        this.ownerDocument = document;
        this.tagName = tagName;
        this.className = className;
        this.children = [];
        this.parentElement = null;
        this.nextElementSibling = null;
        this.previousElementSibling = null;
        this.dataset = {};
        this.listeners = new Map();
        this.hidden = false;
        this.value = "";
    }

    append(...nodes) {
        for (const node of nodes) {
            if (typeof node === "string") {
                continue;
            }
            node.parentElement = this;
            node.previousElementSibling = this.children.at(-1) || null;
            this.children.push(node);
        }
    }

    after(...nodes) {
        const parent = this.parentElement;
        if (!parent) {
            return;
        }
        const index = parent.children.indexOf(this);
        parent.children.splice(index + 1, 0, ...nodes.filter((node) => typeof node !== "string"));
    }

    replaceChildren(...nodes) {
        this.children = [];
        this.append(...nodes);
    }

    querySelector(selector) {
        if (selector.includes(".hnslop-score")) {
            return this.children.find((child) => child.className === "hnslop-score") || null;
        }
        if (selector === ".subline" && this.subline) {
            return this.subline;
        }
        return null;
    }

    querySelectorAll() {
        return [];
    }

    addEventListener(type, listener) {
        this.listeners.set(type, listener);
    }

    dispatchEvent(event) {
        this.listeners.get(event.type)?.(event);
    }

    setAttribute(name, value) {
        this[name] = String(value);
    }

    removeAttribute(name) {
        delete this[name];
    }

    get classList() {
        return {
            contains: (className) => this.className.split(/\s+/).includes(className),
        };
    }
}

class FakeDocument {
    constructor() {
        this.head = new FakeElement(this, "HEAD");
        this.pageTop = new FakeElement(this, "SPAN");
        this.input = null;
        this.row = new FakeElement(this, "TR");
        this.row.id = "1";
        this.metadata = new FakeElement(this, "TR");
        this.subline = new FakeElement(this, "SPAN");
        this.spacer = new FakeElement(this, "TR", "spacer");
        this.row.nextElementSibling = this.metadata;
        this.metadata.nextElementSibling = this.spacer;
        this.metadata.subline = this.subline;
    }

    createElement(tagName) {
        const element = new FakeElement(this, tagName.toUpperCase());
        if (tagName === "input") {
            this.input = element;
        }
        return element;
    }

    getElementById() {
        return null;
    }

    querySelector(selector) {
        return selector === ".pagetop" ? this.pageTop : null;
    }

    querySelectorAll() {
        return [this.row];
    }
}

async function runClient(path, options) {
    const document = new FakeDocument();
    const context = {
        console: {
            info() {},
            warn() {},
            error() {},
        },
        document,
        location: {
            href: "https://news.ycombinator.com/",
            hostname: "news.ycombinator.com",
            pathname: "/",
            search: "",
        },
        URLSearchParams,
        setTimeout,
        clearTimeout,
        AbortController,
    };

    if (path === "userscript.js") {
        context.GM_getValue = (key, fallback) => options.store[key] ?? fallback;
        context.GM_setValue = (key, value) => {
            options.store[key] = value;
        };
        context.GM_xmlhttpRequest = ({ onload }) => {
            onload({
                status: 200,
                response: POST,
                responseHeaders: "",
            });
        };
    } else {
        context.browser = {
            storage: {
                local: {
                    get: async (key) => ({ [key]: options.store[key] }),
                    set: async (values) => Object.assign(options.store, values),
                },
            },
            runtime: {
                sendMessage: async () => POST,
            },
        };
    }

    context.globalThis = context;
    vm.runInNewContext(await readFile(path, "utf8"), context, { filename: path });
    await new Promise((resolve) => setImmediate(resolve));
    return { document, row: document.row };
}

for (const client of ["userscript.js", "extension/content.js"]) {
    test(`${client} persists the max AI score setting`, async () => {
        const store = {};
        const first = await runClient(client, { store });

        assert.equal(first.document.input.value, "");
        first.document.input.value = "70";
        first.document.input.dispatchEvent({ type: "input" });
        await new Promise((resolve) => setImmediate(resolve));
        assert.equal(store[STORAGE_KEY], 70);
        assert.equal(first.row.hidden, true);

        first.document.input.value = "101";
        first.document.input.dispatchEvent({ type: "input" });
        await new Promise((resolve) => setImmediate(resolve));
        assert.equal(store[STORAGE_KEY], 70);
        assert.equal(first.row.hidden, true);

        const invalidStore = { [STORAGE_KEY]: 101 };
        const invalid = await runClient(client, { store: invalidStore });
        assert.equal(invalid.document.input.value, "");
        assert.equal(invalid.row.hidden, false);

        const second = await runClient(client, { store });
        assert.equal(second.document.input.value, "70");
        assert.equal(second.row.hidden, true);

        second.document.input.value = "";
        second.document.input.dispatchEvent({ type: "input" });
        await new Promise((resolve) => setImmediate(resolve));
        assert.equal(store[STORAGE_KEY], null);
        assert.equal(second.row.hidden, false);
    });
}
