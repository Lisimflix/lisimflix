import type { MediaVersion, Resume } from "../api/client";

// Minutes remaining, rounded up; null when there's no resume point or no known duration (shared-file episodes).
export function minutesLeft(resume: Resume | null, durationMs: number | null): number | null {
  if (!resume || durationMs === null) return null;
  return Math.max(1, Math.ceil((durationMs - resume.positionMs) / 60000));
}

export function formatClock(ms: number): string {
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}

// Binary units under the usual GB/MB names, as file managers show them.
export function formatBytes(n: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : Math.round(v * 10) / 10} ${units[i]}`;
}

// Width or height decides, so a cropped 1920x800 film is still 1080p.
export function resolutionName(v: MediaVersion): "4K" | "1080p" | "720p" | "SD" | null {
  const s = v.video[0];
  if (!s) return null;
  if (s.width >= 3600 || s.height >= 2000) return "4K";
  if (s.width >= 1800 || s.height >= 1000) return "1080p";
  if (s.width >= 1200 || s.height >= 700) return "720p";
  return "SD";
}

const CODECS: Record<string, string> = { h264: "H.264", hevc: "HEVC", av1: "AV1", vp9: "VP9", mpeg2video: "MPEG-2", mpeg4: "MPEG-4" };

export const codecName = (codec: string) => CODECS[codec] ?? codec.toUpperCase();

// "Director's Cut · 4K · HEVC · 22.4 GB (file.mkv)"; the file name only ever arrives for admins.
export function versionLabel(v: MediaVersion): string {
  const parts = [
    v.label,
    resolutionName(v),
    v.video[0] ? codecName(v.video[0].codec) : null,
    v.sizeBytes === null ? null : formatBytes(v.sizeBytes),
  ].filter((p): p is string => p !== null);
  return parts.join(" · ") + (v.fileName ? ` (${v.fileName})` : "");
}
