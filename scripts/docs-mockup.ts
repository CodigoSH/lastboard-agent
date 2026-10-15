/**
 * Renders the two pictures of the agent that come from Lastboard's screen:
 *
 *   assets/mockup.png       the image the README and the project page lead with
 *   docs/assets/widget.png  the Docker widget on its own, which the front page
 *                           of the documentation opens with
 *
 *   bun scripts/docs-mockup.ts                 # Lastboard checked out at ../Lastboard
 *   LASTBOARD=/path/to/Lastboard bun scripts/docs-mockup.ts
 *
 * The agent has no screen of its own, so what is photographed is what it makes
 * possible: Lastboard's Docker widget listing two hosts, each reached through
 * an agent built from this repository. The hosts are not real. A small Docker
 * API is served on two Unix sockets with containers invented for the picture,
 * and the agents are pointed at those sockets exactly as they would be at
 * /var/run/docker.sock — so every row on the screen went through this code, and
 * nobody's actual containers end up in a published image.
 *
 * Both programs are built before anything is photographed: a picture of the
 * interface as it was yesterday is worse than none, because nobody doubts it.
 */
import { readFileSync, mkdirSync, rmSync, writeFileSync, existsSync, copyFileSync } from 'node:fs';
import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { createServer } from 'node:net';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';

const OUT = 'assets/mockup.png';
const DOC_OUT = 'docs/assets/widget.png';
const WIDTH = 1600, HEIGHT = 900, DPR = 2;
const LASTBOARD = resolve(process.env.LASTBOARD || '../Lastboard');
const LASTBOARD_BIN = join(LASTBOARD, 'bin/lastboard');
const WORK = join(tmpdir(), 'lastboard-agent-mockup');

// The house look for every published picture.
const ACCENT = '#e8520e', LANG = 'en', THEME = 'dark';

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const dataUri = (path: string, mime: string) =>
    `data:${mime};base64,${readFileSync(path).toString('base64')}`;

/** A port nobody is listening on, so two runs never meet each other. */
function freePort(): Promise<number> {
    return new Promise((res, rej) => {
        const s = createServer();
        s.once('error', rej);
        s.listen(0, '127.0.0.1', () => {
            const port = (s.address() as any).port;
            s.close(() => res(port));
        });
    });
}

async function waitFor(url: string, what: string) {
    for (let i = 0; i < 80; i++) {
        try { if ((await fetch(url)).ok) return; } catch { }
        await sleep(250);
    }
    throw new Error(`${what} never answered on ${url}`);
}

// ── The two hosts ───────────────────────────────────────────────────────────

interface FakeContainer {
    name: string; image: string; running: boolean; up: string;
    ports?: number[]; cpu: number; memMiB: number;
}

const HOSTS: { id: string; name: string; containers: FakeContainer[] }[] = [
    {
        id: 'nas', name: 'Home server', containers: [
            { name: 'jellyfin', image: 'jellyfin/jellyfin:10.10', running: true, up: 'Up 12 days', ports: [8096], cpu: 6.4, memMiB: 812 },
            { name: 'immich-server', image: 'ghcr.io/immich-app/immich-server:release', running: true, up: 'Up 12 days', ports: [2283], cpu: 3.1, memMiB: 1240 },
            { name: 'immich-postgres', image: 'postgres:16-alpine', running: true, up: 'Up 12 days', cpu: 0.8, memMiB: 214 },
            { name: 'vaultwarden', image: 'vaultwarden/server:1.32', running: true, up: 'Up 12 days', ports: [8222], cpu: 0.2, memMiB: 38 },
            { name: 'syncthing', image: 'syncthing/syncthing:1.29', running: true, up: 'Up 4 days', ports: [8384], cpu: 1.7, memMiB: 96 },
            { name: 'nextcloud', image: 'nextcloud:30-apache', running: false, up: 'Exited (0) 2 hours ago', cpu: 0, memMiB: 0 },
        ],
    },
    {
        id: 'vps', name: 'Cloud VPS', containers: [
            { name: 'caddy', image: 'caddy:2-alpine', running: true, up: 'Up 31 days', ports: [443], cpu: 0.4, memMiB: 42 },
            { name: 'uptime-kuma', image: 'louislam/uptime-kuma:1', running: true, up: 'Up 31 days', ports: [3001], cpu: 2.2, memMiB: 148 },
            { name: 'freshrss', image: 'freshrss/freshrss:1.25', running: true, up: 'Up 9 days', ports: [8080], cpu: 0.3, memMiB: 61 },
        ],
    },
];

const idOf = (host: string, name: string) =>
    Array.from(new Bun.CryptoHasher('sha256').update(`${host}/${name}`).digest('hex')).join('');

/**
 * The part of the Docker Engine API the agent forwards, answering for one
 * invented host. Stats carry a delta between two readings, as Docker's do, so
 * the dashboard's CPU arithmetic is the real one.
 */
function serveFakeDocker(socket: string, host: typeof HOSTS[number]) {
    const byId = (id: string) => host.containers.find((c) => idOf(host.id, c.name).startsWith(id));
    return Bun.serve({
        unix: socket,
        fetch(req) {
            const path = new URL(req.url).pathname;
            if (req.method === 'GET' && path === '/containers/json') {
                return Response.json(host.containers.map((c, i) => ({
                    Id: idOf(host.id, c.name),
                    Names: [`/${c.name}`],
                    Image: c.image,
                    State: c.running ? 'running' : 'exited',
                    Status: c.up,
                    Created: 1756000000 + i * 3600,
                    Ports: (c.ports || []).map((p) => ({ IP: '0.0.0.0', PrivatePort: p, PublicPort: p, Type: 'tcp' })),
                    Labels: {},
                })));
            }
            const stats = path.match(/^\/containers\/([^/]+)\/stats$/);
            if (req.method === 'GET' && stats) {
                const c = byId(stats[1]);
                if (!c) return new Response('{"message":"No such container"}', { status: 404 });
                const system = 1_000_000_000, cpus = 4;
                return Response.json({
                    cpu_stats: { cpu_usage: { total_usage: 5_000_000_000 + Math.round(c.cpu / 100 / cpus * system) }, system_cpu_usage: 90_000_000_000 + system, online_cpus: cpus },
                    precpu_stats: { cpu_usage: { total_usage: 5_000_000_000 }, system_cpu_usage: 90_000_000_000 },
                    memory_stats: { usage: c.memMiB * 1024 * 1024, limit: 16 * 1024 * 1024 * 1024 },
                });
            }
            if (req.method === 'POST' && /^\/containers\/[^/]+\/(start|stop|restart)$/.test(path)) {
                return new Response(null, { status: 204 });
            }
            return new Response('{"message":"page not found"}', { status: 404 });
        },
    });
}

// ── The browser ─────────────────────────────────────────────────────────────

let id = 0;
const pending = new Map<number, (v: any) => void>();
let ws: WebSocket;
function cdp(method: string, params: any = {}): Promise<any> {
    const myId = ++id;
    return new Promise((resolve) => { pending.set(myId, resolve); ws.send(JSON.stringify({ id: myId, method, params })); });
}

async function evaluate(expression: string) {
    const r = await cdp('Runtime.evaluate', { expression: `(async () => { ${expression} })()`, awaitPromise: true, returnByValue: true });
    if (r.result?.exceptionDetails) throw new Error(r.result.exceptionDetails.exception?.description || 'evaluate failed');
    return r.result?.result?.value;
}

/**
 * The Docker widget, searched through every shadow root rather than along one
 * fixed path, so a change in how the board nests its tiles still finds it.
 */
const FIND_DOCKER = `
    const find = (root) => {
        const hit = root.querySelector('widget-docker');
        if (hit) return hit;
        for (const el of root.querySelectorAll('*')) {
            if (el.shadowRoot) { const deep = find(el.shadowRoot); if (deep) return deep; }
        }
        return null;
    };`;

/**
 * Waits until the widget shows every container and every running one has its
 * figures — a condition, not a time. Not getting there is a failure with a
 * name, never a picture taken anyway.
 */
async function waitForWidget(expected: number) {
    let last: any = null;
    for (let i = 0; i < 120; i++) {
        last = await evaluate(`
            ${FIND_DOCKER}
            const w = find(document)?.shadowRoot;
            if (!w) return { rows: 0, loading: -1, error: 'no docker widget on the page' };
            return {
                rows: w.querySelectorAll('.cell-name').length,
                loading: w.querySelectorAll('.stats-text.is-loading').length,
                error: w.querySelector('.state-view .state-text')?.textContent || '',
            };`);
        if (last && last.rows === expected && last.loading === 0) return;
        await sleep(250);
    }
    // What was on screen instead, so the failure says more than its own name.
    const shot = await cdp('Page.captureScreenshot', { format: 'png' });
    writeFileSync(join(WORK, 'failed.png'), Buffer.from(shot.result.data, 'base64'));
    const href = await evaluate(`return location.href;`);
    throw new Error(`the Docker widget never settled at ${href}: ${JSON.stringify(last)} ` +
        `(expected ${expected} rows) — what was on screen is in ${join(WORK, 'failed.png')}`);
}

/**
 * One screen. `touch` is a tablet: touch input at a width where the dashboard
 * still lays out the full grid (below 768px it shows bookmarks only). `fit`
 * crops the height to the board, so the laptop does not frame an empty floor.
 */
async function shoot(app: string, w: number, h: number, dpr: number, touch: boolean, fit: boolean, expected: number, out: string) {
    await cdp('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: dpr, mobile: touch });
    await cdp('Emulation.setTouchEmulationEnabled', { enabled: touch, maxTouchPoints: touch ? 5 : 0 });
    await cdp('Page.navigate', { url: app });
    await waitForWidget(expected);
    await evaluate(`await document.fonts.ready;`);
    await sleep(600); // the entrance animation of the tiles
    if (fit) {
        const bottom = await evaluate(`
            const cards = [...document.querySelector('bookmark-grid').shadowRoot.querySelectorAll('.bookmark-grid__card')];
            if (!cards.length) throw new Error('no cards on the board to fit the height to');
            return Math.ceil(Math.max(...cards.map((c) => c.getBoundingClientRect().bottom)) + 28);`);
        h = Math.min(h, bottom);
        await cdp('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: dpr, mobile: touch });
        await sleep(300);
    }
    const png = await cdp('Page.captureScreenshot', { format: 'png' });
    writeFileSync(out, Buffer.from(png.result.data, 'base64'));
    console.log(`  ✓ ${touch ? 'tablet' : 'laptop'} screen ${w}x${h}@${dpr}`);
}

/**
 * The Docker tile on its own, for the front page of the documentation: shot at
 * the dashboard's own scale, then mounted, centred, on the board's background
 * in the 1440x900 frame every figure of the house documentation shares.
 */
async function shootTile(out: string) {
    const box = await evaluate(`
        ${FIND_DOCKER}
        const card = find(document)?.closest('.bookmark-grid__card');
        if (!card) throw new Error('the Docker widget is not inside a tile');
        const r = card.getBoundingClientRect();
        return { x: r.left, y: r.top, width: r.width, height: r.height };`);
    const png = await cdp('Page.captureScreenshot', { format: 'png', clip: { ...box, scale: 1 } });
    writeFileSync(out, Buffer.from(png.result.data, 'base64'));

    const mount = `
import sys
from PIL import Image
src = Image.open(sys.argv[1]).convert('RGBA')
W, H, MARGIN = 1440, 900, 72
BG = (17, 17, 18, 255)                     # the board's own background
k = min((W - 2 * MARGIN) / src.width, (H - 2 * MARGIN) / src.height)
piece = src.resize((round(src.width * k), round(src.height * k)), Image.LANCZOS)
canvas = Image.new('RGBA', (W, H), BG)
canvas.paste(piece, ((W - piece.width) // 2, (H - piece.height) // 2), piece)
canvas.convert('RGB').save(sys.argv[1])
`;
    const r = spawnSync('python3', ['-I', '-c', mount, out], { encoding: 'utf8' });
    if (r.status !== 0) throw new Error(`mounting the tile failed (is Pillow installed?): ${r.stderr.trim()}`);
    mkdirSync(dirname(DOC_OUT), { recursive: true });
    copyFileSync(out, DOC_OUT);
    console.log(`  ✓ ${DOC_OUT} 1440x900`);
}

// ── The run ─────────────────────────────────────────────────────────────────

async function main() {
    console.log('· building the agent and Lastboard, so the picture is of this code');
    rmSync(WORK, { recursive: true, force: true });
    mkdirSync(WORK, { recursive: true });
    const agentBin = join(WORK, 'lastboard-agent');
    if (spawnSync('go', ['build', '-ldflags', '-X main.version=mockup', '-o', agentBin, '.'], { stdio: 'inherit' }).status !== 0) {
        throw new Error('go build failed');
    }
    if (!existsSync(join(LASTBOARD, 'Makefile'))) throw new Error(`no Lastboard checkout at ${LASTBOARD} — set LASTBOARD`);
    if (spawnSync('make', ['build'], { cwd: LASTBOARD, stdio: 'inherit' }).status !== 0) {
        throw new Error('make build failed in the Lastboard checkout');
    }

    const procs: ChildProcess[] = [];
    const closers: (() => void)[] = [];
    const stop = () => {
        procs.forEach((p) => { try { p.kill(); } catch { } });
        closers.forEach((c) => { try { c(); } catch { } });
    };
    process.on('exit', stop);
    for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP'] as const) process.on(sig, () => { stop(); process.exit(1); });

    // Two invented hosts, each behind its own agent.
    const agents: { host: typeof HOSTS[number]; port: number; token: string }[] = [];
    for (const host of HOSTS) {
        const socket = join(WORK, `${host.id}.sock`);
        const daemon = serveFakeDocker(socket, host);
        closers.push(() => daemon.stop(true));
        const port = await freePort();
        const token = Array.from(crypto.getRandomValues(new Uint8Array(32)), (b) => b.toString(16).padStart(2, '0')).join('');
        procs.push(spawn(agentBin, [], { env: { ...process.env, TOKEN: token, PORT: String(port), SOCKET_PATH: socket }, stdio: 'ignore' }));
        await waitFor(`http://127.0.0.1:${port}/health`, `the agent for ${host.name}`);
        agents.push({ host, port, token });
    }

    // A throwaway Lastboard with one board: the Docker widget, and the two
    // widgets that need nothing from outside this machine.
    const lbPort = await freePort();
    const app = `http://localhost:${lbPort}/`;
    const db = join(WORK, 'lastboard.db');
    procs.push(spawn(LASTBOARD_BIN, [], { env: { ...process.env, DB_FILE: db, PORT: String(lbPort) }, stdio: 'ignore' }));
    await waitFor(app, 'Lastboard');

    await fetch(`${app}api/setup`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: 'demo', password: 'Lastboard123!', language: LANG, theme: THEME, accent_color: ACCENT }),
    });
    const login = await fetch(`${app}api/login`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: 'demo', password: 'Lastboard123!' }),
    });
    const cookies = Object.fromEntries((login.headers.getSetCookie?.() || []).map((c) => c.split(';')[0].split('=')));
    if (!cookies.session_token) throw new Error('could not sign in to the throwaway Lastboard');

    const { Database } = await import('bun:sqlite');
    const sql = new Database(db);
    sql.run(`DELETE FROM items`);
    sql.run(`INSERT INTO dashboards (user_id, name, position, is_default) VALUES (1, 'Main dashboard', 0, 1)`);
    const board = (sql.query(`SELECT id FROM dashboards WHERE is_default = 1`).get() as { id: number }).id;
    const widget = (x: number, y: number, w: number, h: number, content: unknown) =>
        sql.run(`INSERT INTO items (user_id, type, x, y, w, h, content, dashboard_id) VALUES (1, 'widget', ?, ?, ?, ?, ?, ?)`,
            [x, y, w, h, JSON.stringify(content), board]);
    widget(1, 1, 8, 5, {
        widgetId: 'docker',
        title: 'Containers',
        hosts: agents.map(({ host, port, token }) => ({ id: host.id, name: host.name, type: 'agent', url: `127.0.0.1:${port}`, token })),
        activeHost: agents[0].host.id,
    });
    widget(9, 1, 4, 2, { widgetId: 'clock', city: 'Lisbon', timezone: 'Europe/Lisbon', hour12: false, showDate: true, showSeconds: false, showCity: true });
    widget(9, 3, 4, 3, { widgetId: 'telemetry', interval: 2000 });
    sql.close();

    const expected = HOSTS.reduce((n, h) => n + h.containers.length, 0);

    procs.push(spawn('google-chrome', [
        '--headless=new', `--remote-debugging-port=${await freePort().then((p) => { (globalThis as any).CDP = p; return p; })}`,
        `--user-data-dir=${join(WORK, 'profile')}`, '--no-first-run', '--hide-scrollbars',
        '--force-color-profile=srgb', 'about:blank',
    ], { stdio: 'ignore' }));
    const CDP = (globalThis as any).CDP as number;
    await waitFor(`http://127.0.0.1:${CDP}/json/version`, 'Chrome');
    const list = await (await fetch(`http://127.0.0.1:${CDP}/json/list`)).json();
    ws = new WebSocket(list.find((t: any) => t.type === 'page').webSocketDebuggerUrl);
    await new Promise((r) => { ws.onopen = () => r(null); });
    ws.onmessage = (e) => {
        const m = JSON.parse(e.data as string);
        if (m.id && pending.has(m.id)) { pending.get(m.id)!(m); pending.delete(m.id); }
    };
    await cdp('Page.enable'); await cdp('Runtime.enable'); await cdp('Network.enable');
    await cdp('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
    for (const [name, value] of Object.entries(cookies)) {
        await cdp('Network.setCookie', { name, value, domain: 'localhost', path: '/' });
    }

    console.log('▶ photographing the board…');
    const desktop = join(WORK, 'desktop.png'), tablet = join(WORK, 'tablet.png');
    await shoot(app, 1280, 800, 2, false, true, expected, desktop);
    await shootTile(join(WORK, 'tile.png'));
    await shoot(app, 1180, 820, 2, true, false, expected, tablet);

    // The screens are inlined rather than linked: a file:// page that goes
    // looking for them mid-render is a composition that sometimes ships with
    // empty frames.
    const html = readFileSync('scripts/docs-mockup.html', 'utf8')
        .replace('__LOGO__', dataUri('assets/logo.png', 'image/png'))
        .replace('__DESKTOP__', dataUri(desktop, 'image/png'))
        .replace('__TABLET__', dataUri(tablet, 'image/png'));
    const page = join(WORK, 'mockup.html');
    writeFileSync(page, html);

    await cdp('Emulation.setTouchEmulationEnabled', { enabled: false, maxTouchPoints: 0 });
    await cdp('Emulation.setDeviceMetricsOverride', { width: WIDTH, height: HEIGHT, deviceScaleFactor: DPR, mobile: false });
    await cdp('Page.navigate', { url: `file://${page}` });
    await sleep(800);
    await evaluate(`await document.fonts.ready;`);
    await sleep(300);
    const png = await cdp('Page.captureScreenshot', { format: 'png' });
    writeFileSync(OUT, Buffer.from(png.result.data, 'base64'));
    console.log(`✓ ${OUT} ${WIDTH * DPR}x${HEIGHT * DPR}`);
    process.exit(0);
}

main().catch((err) => { console.error('✗', err.message); process.exit(1); });
