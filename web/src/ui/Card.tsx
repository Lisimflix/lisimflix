import { useState } from "react";
import type { ItemSummary } from "../api/client";
import { Link } from "../app/router";
import { minutesLeft } from "../browse/format";

// Title, then the year or, for an episode, "S1 E4".
const subtitle = (i: ItemSummary) =>
  i.kind === "episode" && i.seasonIndex !== null && i.index !== null ? `S${i.seasonIndex} E${i.index}` : i.year === null ? "" : String(i.year);

export function Card({ item, href, size }: { item: ItemSummary; href: string; size: "poster" | "wide" }) {
  const [broken, setBroken] = useState(false);
  const state = item.userState;
  const watched = state?.watched?.value === true;
  const resume = state?.resume ?? null;
  const left = minutesLeft(resume, item.durationMs);
  const progress = resume && item.durationMs ? Math.min(100, Math.round((resume.positionMs / item.durationMs) * 100)) : null;
  const unmatched = item.matched === false;
  const offline = item.available === false;
  const name = [
    item.title,
    watched && "watched",
    unmatched && "unmatched",
    offline && "file offline",
    left !== null && `${left} min left`,
  ].filter(Boolean).join(", ");
  return (
    <Link href={href} className={`card ${size}`} aria-label={name}>
      <span className="art">
        {item.primaryImageUrl && !broken ? (
          <img src={item.primaryImageUrl} alt="" loading="lazy" onError={() => setBroken(true)} />
        ) : (
          <span className="noart">No artwork</span>
        )}
        {watched && <span className="badge tick">✓</span>}
        {unmatched && <span className="badge warn">Unmatched</span>}
        {offline && <span className="badge off">File offline</span>}
        {progress !== null && (
          <span className="bar" role="progressbar" aria-label="Progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress}>
            <span style={{ width: `${progress}%` }} />
          </span>
        )}
      </span>
      <span className="cap">
        <span className="t">{item.title}</span>
        <span className="s">{subtitle(item)}</span>
      </span>
    </Link>
  );
}
