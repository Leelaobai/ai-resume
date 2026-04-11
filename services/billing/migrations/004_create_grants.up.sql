CREATE TABLE credit_grants (
  id             VARCHAR(36)  NOT NULL COMMENT 'ULID',
  user_id        VARCHAR(36)  NOT NULL,
  type           ENUM('registration','promotion','referral') NOT NULL,
  credits        BIGINT       NOT NULL COMMENT '初始赠送积分总量',
  remaining      BIGINT       NOT NULL COMMENT '剩余未消耗积分',
  expires_at     DATETIME(3)  NOT NULL COMMENT '过期时间',
  expired_at     DATETIME(3)  NULL     COMMENT '实际过期处理时间',
  transaction_id VARCHAR(36)  NULL     COMMENT '发放时对应的 credit_transactions.id',
  created_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),

  PRIMARY KEY (id),
  KEY idx_cg_user_active   (user_id, expires_at, remaining),
  KEY idx_cg_expire_job    (expires_at, remaining)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='赠送积分包';
