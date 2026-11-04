import type { MediaVersion, Resume, UserItemState } from "../api/client";

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

// Decision 9: a resume point is reused silently only on the same version and source revision, and inside the file.
export type ResumeChoice = { kind: "none" } | { kind: "resume"; positionMs: number } | { kind: "confirm"; positionMs: number };

export function resumeChoice(state: UserItemState | null, version: MediaVersion): ResumeChoice {
  const r = state?.resume;
  if (!r) return { kind: "none" };
  const same = r.versionId === version.id && r.sourceRevision === version.sourceRevision && r.positionMs < (version.durationMs ?? Infinity);
  return { kind: same ? "resume" : "confirm", positionMs: r.positionMs };
}

// shortcut: 639-2→1 table covers common codes only; widen when a library shows raw codes
const ISO639_1: Record<string, string> = {
  eng: "en", jpn: "ja", deu: "de", ger: "de", fra: "fr", fre: "fr", spa: "es", ita: "it", por: "pt", rus: "ru",
  zho: "zh", chi: "zh", kor: "ko", nld: "nl", dut: "nl", swe: "sv", nor: "no", dan: "da", fin: "fi", pol: "pl",
  tur: "tr", ara: "ar", heb: "he", hin: "hi", tha: "th", ces: "cs", cze: "cs", hun: "hu", ell: "el", gre: "el",
  ukr: "uk", vie: "vi", ind: "id", ron: "ro", rum: "ro", bul: "bg", hrv: "hr", fas: "fa", per: "fa", cat: "ca",
};
const languages = new Intl.DisplayNames(["en"], { type: "language" });
export function languageName(code: string): string {
  const two = ISO639_1[code];
  if (!two) return code;
  try {
    return languages.of(two) ?? code;
  } catch {
    return code;
  }
}
