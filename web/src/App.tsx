import { useCallback, useMemo, useState } from "react";
import { Login } from "./Login.tsx";
import { forgetToken, newClient, saveToken, savedToken, type Session } from "./session.ts";
import { Shell } from "./Shell.tsx";

export function App() {
  const [session, setSession] = useState<Session | undefined>();
  // A token from this tab's earlier visit, checked again before use.
  const [resume, setResume] = useState(() => savedToken());
  const [notice, setNotice] = useState<string | undefined>();

  const signOut = useCallback((why?: string) => {
    forgetToken();
    setResume(undefined);
    setSession(undefined);
    setNotice(why);
  }, []);

  const client = useMemo(
    () => session && newClient(session.token, () => signOut("Your session ended: the token was revoked or expired.")),
    [session, signOut],
  );

  if (!session || !client) {
    return (
      <Login
        resume={resume}
        notice={notice}
        onSignedIn={(s) => {
          saveToken(s.token);
          setNotice(undefined);
          setSession(s);
        }}
        onRejected={() => {
          forgetToken();
          setResume(undefined);
        }}
      />
    );
  }
  return (
    <Shell
      session={session}
      client={client}
      onSignOut={signOut}
      onSession={(s) => {
        // A rotation: the new token from now on, here and after a reload.
        saveToken(s.token);
        setSession(s);
      }}
    />
  );
}
