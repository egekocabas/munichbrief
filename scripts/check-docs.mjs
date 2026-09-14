#!/usr/bin/env node

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, extname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const markdownFiles = findMarkdownFiles(repositoryRoot)
  .map((file) => relative(repositoryRoot, file))
  .sort();

const failures = [];

for (const markdownFile of markdownFiles) {
  const absoluteMarkdownFile = resolve(repositoryRoot, markdownFile);
  const contents = readFileSync(absoluteMarkdownFile, "utf8");
  const withoutCodeFences = contents.replace(/```[\s\S]*?```/g, "");

  for (const match of withoutCodeFences.matchAll(/!?\[[^\]]*\]\(([^)]+)\)/g)) {
    const destination = match[1].trim().replace(/^<|>$/g, "");
    if (!destination || /^(?:https?:|mailto:)/i.test(destination)) {
      continue;
    }

    const [rawPath, rawFragment = ""] = destination.split("#", 2);
    const decodedPath = decodeURIComponent(rawPath);
    const target = resolve(dirname(absoluteMarkdownFile), decodedPath || ".");

    if (!target.startsWith(repositoryRoot + "/") && target !== repositoryRoot) {
      failures.push(`${markdownFile}: link escapes the repository: ${destination}`);
      continue;
    }
    if (!existsSync(target)) {
      failures.push(`${markdownFile}: missing local link target: ${destination}`);
      continue;
    }
    if (rawFragment && statSync(target).isFile() && extname(target).toLowerCase() === ".md") {
      const anchors = markdownAnchors(readFileSync(target, "utf8"));
      const expected = decodeURIComponent(rawFragment).toLowerCase();
      if (!anchors.has(expected)) {
        failures.push(`${markdownFile}: missing heading #${rawFragment} in ${relative(repositoryRoot, target)}`);
      }
    }
  }
}

if (failures.length > 0) {
  console.error(failures.join("\n"));
  process.exit(1);
}

console.log(`Checked local links in ${markdownFiles.length} Markdown files.`);

function findMarkdownFiles(directory) {
  const files = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.isDirectory() && [".git", ".local", ".data", "node_modules"].includes(entry.name)) {
      continue;
    }
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...findMarkdownFiles(path));
    } else if (entry.isFile() && extname(entry.name).toLowerCase() === ".md") {
      files.push(path);
    }
  }
  return files;
}

function markdownAnchors(contents) {
  const anchors = new Set();
  const occurrences = new Map();

  for (const line of contents.replace(/```[\s\S]*?```/g, "").split("\n")) {
    const match = line.match(/^#{1,6}\s+(.+?)\s*#*$/);
    if (!match) {
      continue;
    }
    const base = match[1]
      .toLowerCase()
      .replace(/<[^>]+>/g, "")
      .replace(/[`*_~]/g, "")
      .replace(/[^\p{L}\p{N}\s-]/gu, "")
      .trim()
      .replace(/\s+/g, "-");
    const occurrence = occurrences.get(base) ?? 0;
    occurrences.set(base, occurrence + 1);
    anchors.add(occurrence === 0 ? base : `${base}-${occurrence}`);
  }
  return anchors;
}
