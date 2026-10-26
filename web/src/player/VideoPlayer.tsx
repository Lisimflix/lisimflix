import type { M1Client, PlaybackSession } from "../api/client";

interface Props {
  client: M1Client;
  session: PlaybackSession;
  // Called once when playback finishes; error is set when it stopped abnormally.
  onEnd: (error?: string) => void;
}

// Task 1 shell: Task 2 replaces this with autoplay handling, heartbeats and cleanup.
export function VideoPlayer({ session }: Props) {
  return (
    <div className="player">
      <video controls playsInline src={session.contentUrl} />
    </div>
  );
}
