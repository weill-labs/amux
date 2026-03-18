#!/usr/bin/env node
/**
 * cast2gif - Convert asciinema .cast files to GIF using Playwright + asciinema-player.
 *
 * Uses a real browser engine for pixel-perfect Unicode rendering.
 * Instead of seeking (which doesn't render properly), plays the recording
 * at high speed and captures screenshots at regular intervals.
 *
 * Usage:
 *   node cast2gif.mjs input.cast output.gif [--font "Fira Code"] [--font-size 16] [--fps 8]
 */

import { chromium } from 'playwright';
import { readFileSync, writeFileSync, mkdirSync, existsSync, rmSync } from 'fs';
import { resolve, dirname } from 'path';
import { execFileSync } from 'child_process';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));

// Parse args
const args = process.argv.slice(2);
if (args.length < 2) {
  console.error('Usage: node cast2gif.mjs <input.cast> <output.gif> [options]');
  process.exit(1);
}

const inputFile = resolve(args[0]);
const outputFile = resolve(args[1]);

function getOption(name, defaultVal) {
  const idx = args.indexOf(name);
  return idx >= 0 && idx + 1 < args.length ? args[idx + 1] : defaultVal;
}

const fontFamily = getOption('--font', 'Fira Code');
const fontSize = parseInt(getOption('--font-size', '16'));
const fps = parseInt(getOption('--fps', '8'));

// Parse .cast file header for dimensions and duration
const castContent = readFileSync(inputFile, 'utf8');
const lines = castContent.trim().split('\n');
const header = JSON.parse(lines[0]);

// v3: header.term.cols/rows, v2: header.width/height
const cols = header.term?.cols || header.width || 80;
const rows = header.term?.rows || header.height || 24;

// Calculate duration from events (handle both v2 absolute and v3 delta timestamps)
const rawEvents = lines.slice(1).map(l => JSON.parse(l));
let absTime = 0;
let maxTime = 0;
for (const e of rawEvents) {
  if (header.version === 3) {
    absTime += e[0];
  } else {
    absTime = e[0];
  }
  if (e[1] === 'o' && absTime > maxTime) maxTime = absTime;
}
const totalDuration = maxTime;

console.log(`Cast: ${cols}x${rows}, ${rawEvents.length} events, ${totalDuration.toFixed(1)}s`);

// Calculate how many frames we need
const totalFrames = Math.ceil(totalDuration * fps);
console.log(`Capturing ${totalFrames} frames at ${fps} fps (${(totalDuration).toFixed(1)}s playback)...`);

// Create frames directory
const framesDir = '/tmp/cast2gif-frames';
if (existsSync(framesDir)) rmSync(framesDir, { recursive: true });
mkdirSync(framesDir, { recursive: true });

async function main() {
  const browser = await chromium.launch({ headless: true });
  const scale = parseFloat(getOption('--scale', '2'));
  const context = await browser.newContext({ deviceScaleFactor: scale });
  const page = await context.newPage();

  // Build HTML with asciinema-player — use local file URL for the cast data
  const castDataPath = `${framesDir}/recording.cast`;
  writeFileSync(castDataPath, castContent);

  const playerHtml = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<link rel="stylesheet" type="text/css" href="https://unpkg.com/asciinema-player@3.8.0/dist/bundle/asciinema-player.css" />
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { background: #1e1e2e; display: inline-block; }
  #container { display: inline-block; }
  .ap-control-bar { display: none !important; }
  .ap-start-button { display: none !important; }
  .ap-overlay { display: none !important; }
  .ap-player { cursor: default !important; }
</style>
</head>
<body>
<div id="container"></div>
<script src="https://unpkg.com/asciinema-player@3.8.0/dist/bundle/asciinema-player.min.js"><\/script>
<script>
  const castData = ${JSON.stringify(castContent)};
  const blob = new Blob([castData], { type: 'text/plain' });
  const castUrl = URL.createObjectURL(blob);

  const player = AsciinemaPlayer.create(
    castUrl,
    document.getElementById('container'),
    {
      fit: false,
      autoPlay: false,
      speed: 1,
      terminalFontFamily: "'${fontFamily}', 'Noto Sans Symbols 2', 'Noto Emoji', 'Menlo', monospace",
      terminalFontSize: '${fontSize}px',
      cols: ${cols},
      rows: ${rows},
      theme: 'asciinema',
      idleTimeLimit: 999,
    }
  );

  window.__player = player;
  window.__ready = false;
  window.__done = false;
  window.__currentTime = 0;

  player.addEventListener('playing', () => { window.__ready = true; });
  player.addEventListener('ended', () => { window.__done = true; });

  // Track current time via polling (player doesn't expose a sync getter)
  setInterval(() => {
    try { window.__currentTime = player.getCurrentTime(); } catch(e) {}
  }, 10);
</script>
</body>
</html>`;

  const htmlPath = `${framesDir}/player.html`;
  writeFileSync(htmlPath, playerHtml);

  await page.goto(`file://${htmlPath}`, { waitUntil: 'networkidle' });

  // Wait for player to initialize
  await page.waitForSelector('.ap-player', { timeout: 15000 });
  await page.waitForTimeout(2000); // let fonts load

  const terminal = await page.$('.ap-player');
  if (!terminal) {
    console.error('Could not find terminal element');
    await browser.close();
    process.exit(1);
  }

  // Start playback at 100x speed — we screenshot at calculated time points
  await page.evaluate(() => { window.__player.play(); });
  await page.waitForFunction(() => window.__ready, { timeout: 5000 });

  // Capture frames by waiting for the player to reach each time point
  const frameInterval = 1.0 / fps;
  let frameIndex = 0;

  for (let targetTime = 0; targetTime <= totalDuration; targetTime += frameInterval) {
    // Wait until player reaches this time (at 100x speed, it's fast)
    const deadline = Date.now() + 30000; // 30s timeout
    while (true) {
      const currentTime = await page.evaluate(() => window.__currentTime);
      const done = await page.evaluate(() => window.__done);
      if (currentTime >= targetTime || done) break;
      if (Date.now() > deadline) {
        console.warn(`\nTimeout waiting for t=${targetTime.toFixed(1)}s (at ${currentTime.toFixed(1)}s)`);
        break;
      }
      await page.waitForTimeout(5);
    }

    // Let rendering settle
    await page.waitForTimeout(20);

    // Screenshot
    const framePath = `${framesDir}/frame_${String(frameIndex).padStart(5, '0')}.png`;
    await terminal.screenshot({ path: framePath });
    frameIndex++;

    if (frameIndex % 10 === 0 || targetTime + frameInterval > totalDuration) {
      process.stdout.write(`\rFrame ${frameIndex}/${totalFrames} (${targetTime.toFixed(1)}s)`);
    }
  }
  console.log(`\n${frameIndex} frames captured.`);

  await browser.close();

  // Convert frames to GIF using ffmpeg with palette for good quality
  console.log('Assembling GIF...');

  const palettePath = `${framesDir}/palette.png`;

  execFileSync('ffmpeg', [
    '-y', '-framerate', String(fps),
    '-i', `${framesDir}/frame_%05d.png`,
    '-vf', 'palettegen=max_colors=256:stats_mode=diff',
    palettePath,
  ], { stdio: 'pipe' });

  execFileSync('ffmpeg', [
    '-y', '-framerate', String(fps),
    '-i', `${framesDir}/frame_%05d.png`,
    '-i', palettePath,
    '-lavfi', 'paletteuse=dither=bayer:bayer_scale=5',
    outputFile,
  ], { stdio: 'pipe' });

  const { statSync } = await import('fs');
  const size = (statSync(outputFile).size / 1024).toFixed(0);
  console.log(`Done! ${outputFile} (${size}KB)`);

  // Clean up frames
  rmSync(framesDir, { recursive: true });
}

main().catch(err => {
  console.error('Error:', err.message);
  process.exit(1);
});
