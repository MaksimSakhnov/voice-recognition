create extension if not exists pgcrypto;

create type job_status as enum ('queued', 'processing', 'completed', 'failed');

create table jobs (
    id uuid primary key default gen_random_uuid(),
    original_filename text not null check (length(original_filename) > 0),
    audio_path text not null unique,
    language varchar(16) not null default 'ru',
    status job_status not null default 'queued',
    transcript text,
    error_message text,
    created_at timestamptz not null default now(),
    started_at timestamptz,
    finished_at timestamptz,
    constraint jobs_completed_has_transcript check (status <> 'completed' or transcript is not null),
    constraint jobs_failed_has_error check (status <> 'failed' or error_message is not null)
);

create index jobs_created_at_idx on jobs (created_at desc);
create index jobs_completed_finished_at_idx on jobs (finished_at desc) where status = 'completed';
