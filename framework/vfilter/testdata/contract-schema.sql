-- VFILTER tables of shared contract 0.3.2, as the website's migrations create them on SQL Server
-- (VFilterWebsite database/migrations/2026_10_17_10000{1,2}_*). Test fixture only: the filter never
-- creates or alters schema in production.
CREATE TABLE filter_modules (
    id int IDENTITY PRIMARY KEY, type nvarchar(16) NOT NULL, name nvarchar(32) NOT NULL UNIQUE,
    module_ip nvarchar(64) NOT NULL, module_port int NOT NULL, guard_ip nvarchar(64) NOT NULL,
    guard_port int NOT NULL, enabled bit NOT NULL DEFAULT 0);
CREATE TABLE filter_settings (
    [key] nvarchar(128) PRIMARY KEY, value nvarchar(max) NOT NULL, value_type nvarchar(16) NOT NULL,
    [group] nvarchar(32) NOT NULL, requires_restart nvarchar(16) NOT NULL DEFAULT 'none',
    updated_at datetime2(3) NULL, updated_by int NULL);
CREATE TABLE filter_lists (
    id bigint IDENTITY PRIMARY KEY, list nvarchar(32) NOT NULL, value nvarchar(256) NOT NULL,
    comment nvarchar(256) NULL, CONSTRAINT filter_lists_list_value_unique UNIQUE (list, value));
CREATE TABLE filter_area_rules (
    id bigint IDENTITY PRIMARY KEY, [rule] nvarchar(48) NOT NULL, area nvarchar(64) NOT NULL,
    target nvarchar(128) NULL, params nvarchar(max) NULL, enabled bit NOT NULL DEFAULT 1);
CREATE TABLE filter_cooldowns (action nvarchar(32) PRIMARY KEY, seconds int NOT NULL DEFAULT 0);
CREATE TABLE filter_ga_permissions (
    username nvarchar(32) PRIMARY KEY,
    make_item bit NOT NULL DEFAULT 0, zoe bit NOT NULL DEFAULT 0, load_monster bit NOT NULL DEFAULT 0,
    mobkill0 bit NOT NULL DEFAULT 0, mobkill1 bit NOT NULL DEFAULT 0, notice bit NOT NULL DEFAULT 0,
    filter_cmd bit NOT NULL DEFAULT 0, invisible bit NOT NULL DEFAULT 0, invincible bit NOT NULL DEFAULT 0,
    ban bit NOT NULL DEFAULT 0, recall_user bit NOT NULL DEFAULT 0, recall_guild bit NOT NULL DEFAULT 0,
    move_to_user bit NOT NULL DEFAULT 0, move_to_npc bit NOT NULL DEFAULT 0, to_town bit NOT NULL DEFAULT 0,
    go_town bit NOT NULL DEFAULT 0, reset_q bit NOT NULL DEFAULT 0, wp bit NOT NULL DEFAULT 0,
    gm_skill bit NOT NULL DEFAULT 0, world_status bit NOT NULL DEFAULT 0);
CREATE TABLE filter_config_version (area nvarchar(16) PRIMARY KEY, version bigint NOT NULL DEFAULT 0, updated_at datetime2(3) NULL);
CREATE TABLE filter_commands (
    id bigint IDENTITY PRIMARY KEY, command nvarchar(32) NOT NULL, target nvarchar(64) NOT NULL,
    payload nvarchar(max) NULL, status nvarchar(16) NOT NULL DEFAULT 'pending', result nvarchar(max) NULL,
    created_by int NOT NULL, created_at datetime2(3) NOT NULL, processed_at datetime2(3) NULL);
CREATE INDEX filter_commands_status_created_at_index ON filter_commands (status, created_at);
CREATE TABLE filter_status (
    module nvarchar(32) PRIMARY KEY, state nvarchar(16) NOT NULL DEFAULT 'stopped',
    online_count int NOT NULL DEFAULT 0, connections int NOT NULL DEFAULT 0, gateway_mode nvarchar(16) NULL,
    version nvarchar(32) NULL, started_at datetime2(3) NULL, heartbeat_at datetime2(3) NULL, last_error nvarchar(max) NULL);
CREATE TABLE filter_sessions (
    id bigint IDENTITY PRIMARY KEY, module nvarchar(32) NOT NULL, ip nvarchar(45) NOT NULL, hwid nvarchar(64) NULL,
    username nvarchar(32) NULL, charname nvarchar(64) NULL, connected_at datetime2(3) NOT NULL, last_packet_at datetime2(3) NULL);
CREATE TABLE filter_logs (
    id bigint IDENTITY PRIMARY KEY, ts datetime2(3) NOT NULL, level nvarchar(8) NOT NULL, module nvarchar(32) NOT NULL,
    category nvarchar(32) NOT NULL, ip nvarchar(45) NULL, hwid nvarchar(64) NULL, username nvarchar(32) NULL,
    charname nvarchar(64) NULL, opcode nvarchar(8) NULL, message nvarchar(1024) NOT NULL, data nvarchar(max) NULL);
CREATE INDEX filter_logs_ts_index ON filter_logs (ts);
