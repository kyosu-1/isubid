INSERT INTO categories (id, name) VALUES
  (1, 'オフィスチェア'),
  (2, 'ゲーミングチェア'),
  (3, 'アンティーク');

-- 全ユーザーのパスワードは 'password'(bcrypt cost 12)
INSERT INTO users (id, name, password_hash) VALUES
  (1,  'seed_user_01', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (2,  'seed_user_02', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (3,  'seed_user_03', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (4,  'seed_user_04', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (5,  'seed_user_05', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (6,  'seed_user_06', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (7,  'seed_user_07', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (8,  'seed_user_08', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (9,  'seed_user_09', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (10, 'seed_user_10', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (11, 'seed_user_11', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (12, 'seed_user_12', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (13, 'seed_user_13', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (14, 'seed_user_14', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (15, 'seed_user_15', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (16, 'seed_user_16', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (17, 'seed_user_17', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (18, 'seed_user_18', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (19, 'seed_user_19', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK'),
  (20, 'seed_user_20', '$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK');

-- liveオークション10件。ends_at は id 昇順の階段配置(一覧の ends_at ASC 順検証のため)
INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status) VALUES
  (1,  1, 3, 'ヘリテージ・ウィングチェア',       '英国アンティークの本革ウィングチェア', 1000, '2026-01-01 00:00:00', '2030-01-01 01:00:00', 'live'),
  (2,  2, 1, 'エルゴホスト Model E',             '長時間作業向けエルゴノミクスチェア',   2000, '2026-01-01 00:00:00', '2030-01-01 02:00:00', 'live'),
  (3,  3, 2, 'ISUレーサー GT',                   'フルバケット型ゲーミングチェア',       3000, '2026-01-01 00:00:00', '2030-01-01 03:00:00', 'live'),
  (4,  4, 1, 'メッシュフロー 40',                '通気性メッシュのタスクチェア',         4000, '2026-01-01 00:00:00', '2030-01-01 04:00:00', 'live'),
  (5,  5, 3, 'ミッドセンチュリー・ラウンジ',     '1960年代のラウンジチェア',             2500, '2026-01-01 00:00:00', '2030-01-01 05:00:00', 'live'),
  (6,  6, 2, 'ネオンストライク Z',               'RGBライト内蔵ゲーミングチェア',        3000, '2026-01-01 00:00:00', '2030-01-01 06:00:00', 'live'),
  (7,  7, 1, 'スタンドフレックス',               '昇降デスク対応ハイチェア',             3500, '2026-01-01 00:00:00', '2030-01-01 07:00:00', 'live'),
  (8,  8, 3, 'チャーチチェア 1920',              '教会で使われていた木製チェア',         4000, '2026-01-01 00:00:00', '2030-01-01 08:00:00', 'live'),
  (9,  9, 2, 'プロシート・エディション',         'eスポーツチーム監修モデル',            4500, '2026-01-01 00:00:00', '2030-01-01 09:00:00', 'live'),
  (10, 10, 1, 'コンパクトワーク 01',             '省スペース設計のワークチェア',         5000, '2026-01-01 00:00:00', '2030-01-01 10:00:00', 'live');

-- 一覧のstatusフィルタ実証用: closed(落札済み)とupcoming
INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status, winner_id, winning_price) VALUES
  (11, 11, 3, '初代ISUCONチェア', '記念すべき初代モデル(終了済み)', 10000, '2025-12-01 00:00:00', '2026-01-15 00:00:00', 'closed', 12, 12000);

INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status) VALUES
  (12, 12, 1, 'ISUリラックス Pro', '開始前のリクライニングチェア', 8000, '2030-06-01 00:00:00', '2030-06-02 00:00:00', 'upcoming');

-- auction 1: 3件(現在価格1500) / auction 2〜4: 1件ずつ / 5〜10: 0件 / auction 11: 2件
INSERT INTO bids (id, auction_id, user_id, amount, created_at) VALUES
  (1, 1, 2, 1000, '2026-07-01 00:00:00'),
  (2, 1, 3, 1200, '2026-07-01 01:00:00'),
  (3, 1, 4, 1500, '2026-07-01 02:00:00'),
  (4, 2, 5, 2100, '2026-07-01 03:00:00'),
  (5, 3, 6, 3100, '2026-07-01 04:00:00'),
  (6, 4, 7, 4100, '2026-07-01 05:00:00'),
  (7, 11, 13, 10500, '2026-01-10 00:00:00'),
  (8, 11, 12, 12000, '2026-01-14 00:00:00');
