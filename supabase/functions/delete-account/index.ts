// delete-account Edge Function: the only way a user removes themselves. The
// schema revokes client DELETE on profiles/teams/matches, so deletion has to
// go through the auth user — and deleting auth.users cascades the whole chain
// (auth.users -> profiles -> teams -> matches, every FK in 0001_init.sql on
// that path is ON DELETE CASCADE).
//
// The account deleted is always the one that signed the request. The user id
// comes from the verified session and the body is never consulted for an
// identity, so there is no request shape that names somebody else's account.
//
// Body: { confirm: 'DELETE' }

import { createClient } from "npm:@supabase/supabase-js@2";

// Deletion is irreversible and takes the team and its match history with it,
// so the body has to say so out loud. One string compare; it costs nothing and
// it means a stray fetch or a mis-wired button can't empty an account.
const CONFIRM = "DELETE";

const CORS = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Headers":
    "authorization, x-client-info, apikey, content-type",
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { ...CORS, "Content-Type": "application/json" },
  });
}

Deno.serve(async (req) => {
  if (req.method === "OPTIONS") return new Response("ok", { headers: CORS });
  if (req.method !== "POST") return json({ error: "POST only" }, 405);

  const url = Deno.env.get("SUPABASE_URL")!;
  // Caller-scoped client for auth; service client for the privileged delete
  // (the admin API is service-role only).
  const auth = createClient(url, Deno.env.get("SUPABASE_ANON_KEY")!, {
    global: { headers: { Authorization: req.headers.get("Authorization") ?? "" } },
  });
  const service = createClient(url, Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!);

  const { data: { user } } = await auth.auth.getUser();
  if (!user) return json({ error: "not signed in" }, 401);

  let body: { confirm?: string };
  try {
    body = await req.json();
  } catch {
    return json({ error: "invalid JSON body" }, 400);
  }
  if (body.confirm !== CONFIRM) {
    return json(
      { error: `send { "confirm": "${CONFIRM}" } to delete your account` },
      400,
    );
  }

  // user.id, and nothing else, ever. A hard delete on purpose: a soft delete
  // leaves the auth.users row in place, so nothing would cascade and the
  // profile/team/matches would outlive the account.
  const { error } = await service.auth.admin.deleteUser(user.id);
  if (error) return json({ error: `delete failed: ${error.message}` }, 500);

  return json({ deleted: true, userId: user.id });
});
