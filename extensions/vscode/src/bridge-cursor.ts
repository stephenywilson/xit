import * as fs from "fs";
import type { AdapterEvent } from "./types";

// Pulled out of extension.ts (which imports "vscode" and so can't be
// required under plain node:test) so this file's resilience — surviving
// truncation/rotation, malformed lines, and bursts of rapid appends — can be
// covered by real behavioral tests instead of only structural/text
// assertions against compiled source.
export interface HookFileCursor {
  offset: number;
  inode?: number;
  mtimeMs: number;
  lastLineSignature?: string;
  remainder: string;
}

export function initializeHookCursor(hookFile: string): HookFileCursor {
  try {
    const stat = fs.statSync(hookFile);
    return {
      offset: stat.size,
      inode: stat.ino,
      mtimeMs: stat.mtimeMs,
      remainder: "",
    };
  } catch {
    return { offset: 0, mtimeMs: 0, remainder: "" };
  }
}

export function readAppendedHookEvents(
  hookFile: string,
  cursor: HookFileCursor,
): AdapterEvent[] {
  try {
    const stat = fs.statSync(hookFile);
    const replaced = cursor.inode !== undefined && stat.ino !== cursor.inode;
    const truncated = stat.size < cursor.offset;
    const start = replaced || truncated ? 0 : cursor.offset;
    if (stat.size === start) {
      cursor.inode = stat.ino;
      cursor.mtimeMs = stat.mtimeMs;
      return [];
    }

    const length = stat.size - start;
    const buffer = Buffer.alloc(length);
    const fd = fs.openSync(hookFile, "r");
    try {
      fs.readSync(fd, buffer, 0, length, start);
    } finally {
      fs.closeSync(fd);
    }

    cursor.offset = stat.size;
    cursor.inode = stat.ino;
    cursor.mtimeMs = stat.mtimeMs;

    const chunks = `${cursor.remainder}${buffer.toString("utf-8")}`.split("\n");
    cursor.remainder = chunks.pop() || "";
    const events: AdapterEvent[] = [];
    for (const line of chunks) {
      if (!line.trim() || line === cursor.lastLineSignature) continue;
      cursor.lastLineSignature = line;
      try {
        events.push(JSON.parse(line) as AdapterEvent);
      } catch {
        // Ignore malformed appended lines; a later complete line will be processed.
      }
    }
    return events;
  } catch {
    return [];
  }
}
