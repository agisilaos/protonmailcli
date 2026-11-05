"""Keep Git transport configuration while querying a resolved endpoint."""

import os


LOOKUP_REMOTE = "protonmailcli-endpoint"


def config_entries(config):
    for setting in config.split("\0"):
        key, separator, value = setting.partition("\n")
        if key:
            yield key, value if separator else "true"


def endpoint_settings(config, alias, remote):
    yield f"remote.{alias}.url", remote
    for key, value in config_entries(config):
        if key in {
            "remote.origin.proxy", "remote.origin.proxyauthmethod",
            "remote.origin.uploadpack", "remote.origin.receivepack",
        }:
            yield f"remote.{alias}.{key.rsplit('.', 1)[1]}", value


def redirect_settings(remote):
    if remote.startswith(("http://", "https://")):
        yield "http.followredirects", "false"
        yield f"http.{remote}.followredirects", "false"


def push_settings(config, alias, remote):
    yield from endpoint_settings(config, alias, remote)
    for key, value in config_entries(config):
        if (key in {"core.sshcommand", "core.gitproxy", "ssh.variant"}
                or (key.startswith(("http.", "https.")) and key.endswith((".proxy", ".proxyauthmethod")))):
            yield key, value
    yield from redirect_settings(remote)


def with_config(environment, settings):
    env = environment.copy()
    for key in list(env):
        if key.startswith(("GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_")):
            del env[key]
    env["GIT_CONFIG_COUNT"] = str(len(settings))
    for index, (key, value) in enumerate(settings):
        env[f"GIT_CONFIG_KEY_{index}"] = key
        env[f"GIT_CONFIG_VALUE_{index}"] = value
    return env


def transport_environment(config, environment, remote):
    transport = list(endpoint_settings(config, LOOKUP_REMOTE, remote))
    for key, value in config_entries(config):
        if (key.startswith(("credential.", "http.", "https.", "protocol.", "ssh."))
                or key in {"core.askpass", "core.sshcommand", "core.gitproxy"}):
            transport.append((key, value))
    transport.extend(redirect_settings(remote))

    env = with_config(environment, transport)
    for key in ("GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_COMMON_DIR", "GIT_WORK_TREE"):
        env.pop(key, None)
    env.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
    return env


def supports_config_hooks(version):
    fields = version.split()
    if len(fields) < 3:
        return False
    major, _, tail = fields[2].partition(".")
    minor = tail.partition(".")[0]
    return major.isdigit() and minor.isdigit() and (int(major), int(minor)) >= (2, 54)
