CREATE TABLE wallets (
  id          VARCHAR(36)  NOT NULL COMMENT 'ULID',
  user_id     VARCHAR(36)  NOT NULL COMMENT '关联 users.id，唯一',
  balance     BIGINT       NOT NULL DEFAULT 0 COMMENT '总余额（积分），含冻结',
  frozen      BIGINT       NOT NULL DEFAULT 0 COMMENT '预扣冻结中的积分',
  total_used  BIGINT       NOT NULL DEFAULT 0 COMMENT '历史累计消耗积分',
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at  DATETIME(3)  NULL COMMENT 'GORM 软删除',

  PRIMARY KEY (id),
  UNIQUE KEY uq_wallets_user_id (user_id),
  CONSTRAINT chk_wallets_balance CHECK (balance >= 0),
  CONSTRAINT chk_wallets_frozen  CHECK (frozen >= 0),
  CONSTRAINT chk_wallets_frozen_lte_balance CHECK (frozen <= balance)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户积分钱包';
