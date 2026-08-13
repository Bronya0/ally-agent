/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
export const buildVersion = (
  typeof __ALLY_BUILD_VERSION__ === 'string' && __ALLY_BUILD_VERSION__
    ? __ALLY_BUILD_VERSION__
    : 'dev'
);
