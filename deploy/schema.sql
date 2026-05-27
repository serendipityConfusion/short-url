CREATE DATABASE IF NOT EXISTS short_url_0 DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE short_url_0;

CREATE TABLE IF NOT EXISTS short_urls_00 (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  url_hash CHAR(64) NOT NULL,
  short_code VARCHAR(32) NOT NULL DEFAULT '',
  original_url TEXT NOT NULL,
  redirect_url TEXT NULL,
  expires_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_url_hash (url_hash),
  KEY idx_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS short_urls_01 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_02 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_03 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_04 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_05 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_06 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_07 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_08 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_09 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_10 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_11 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_12 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_13 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_14 LIKE short_urls_00;
CREATE TABLE IF NOT EXISTS short_urls_15 LIKE short_urls_00;
