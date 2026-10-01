-- Free-tier projects pause after ~7 days idle. The GitHub Action used to ping
-- with an anon REST read, yet Supabase still warned the project was idle, so
-- the ping now calls this function, which does a real write.
--
-- One row, one timestamp. The table has RLS on and no policies, so anon can't
-- touch it directly; the only way in is this security definer function, which
-- can do nothing but bump the timestamp.
create table public.keepalive (
  id int primary key default 1 check (id = 1),
  pinged_at timestamptz not null default now()
);
alter table public.keepalive enable row level security;
insert into public.keepalive default values;

create function public.keepalive() returns timestamptz
language sql security definer set search_path = '' as $$
  update public.keepalive set pinged_at = now() where id = 1 returning pinged_at;
$$;

revoke execute on function public.keepalive() from public;
grant execute on function public.keepalive() to anon;
