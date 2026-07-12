export const buildVersion = (
  typeof __ALLY_BUILD_VERSION__ === 'string' && __ALLY_BUILD_VERSION__
    ? __ALLY_BUILD_VERSION__
    : 'dev'
);
