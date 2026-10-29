import { useRef } from "react";
import { ApiError, newIdempotencyKey, type Client, type PlaybackSession } from "../api/client";

// Shared by M1App and PlayerPage: start a session with the idempotency and generation rules.
export function useStarter(client: Client) {
  // Each load/start takes a new generation; a late response from an older one is dropped.
  const generation = useRef(0);
  // Kept for a retry of the same start; replaced after any answer that settles it.
  const key = useRef<string | null>(null);

  // Resolves null when a newer generation superseded this start (a late session is stopped).
  // Rejects with the ApiError only while this start is still current.
  async function start(itemId: string, versionId: string): Promise<PlaybackSession | null> {
    const gen = ++generation.current;
    key.current ??= newIdempotencyKey();
    try {
      const session = await client.start(itemId, versionId, key.current);
      if (gen !== generation.current) {
        void client.stop(session.id).catch(() => undefined);
        return null;
      }
      key.current = null;
      return session;
    } catch (err) {
      if (gen !== generation.current) return null;
      // Only an unanswered or server-side failure may repeat the same request.
      const retrySame = err instanceof ApiError && (err.status === 0 || err.status >= 500);
      if (!retrySame) key.current = null;
      throw err;
    }
  }

  return { generation, start };
}
