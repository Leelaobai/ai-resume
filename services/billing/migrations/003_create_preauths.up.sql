CREATE TABLE pre_auths (
  id               VARCHAR(36)  NOT NULL COMMENT 'ULID，即 auth_id',
  user_id          VARCHAR(36)  NOT NULL,
  service_name     VARCHAR(64)  NOT NULL COMMENT '发起预扣的服务',
  request_id       VARCHAR(128) NOT NULL COMMENT '幂等键',
  frozen_credits   BIGINT       NOT NULL COMMENT '冻结的积分数量',
  status           ENUM('pending','settled','cancelled') NOT NULL DEFAULT 'pending',
  created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at       DATETIME(3)  NOT NULL COMMENT '过期时间',
  settled_at       DATETIME(3)  NULL,
  transaction_id   VARCHAR(36)  NULL COMMENT '结算后关联的 credit_transactions.id',
  updated_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),

  PRIMARY KEY (id),
  UNIQUE KEY uq_pa_request_id     (request_id),
  KEY idx_pa_user_status          (user_id, status),
  KEY idx_pa_status_expires       (status, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='积分预扣记录';
