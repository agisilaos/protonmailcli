"""Keep Git transport configuration while querying a resolved endpoint."""

import os


def transport_environment(config, environment):
    transport = []
    for setting in config.split("\0"):
        key, separator, value = setting.partition("\n")
        if (key.startswith(("credential.", "http.", "https.", "protocol.", "ssh."))
                or key in {"core.askpass", "core.sshcommand", "core.gitproxy"}):
            transport.append((key, value if separator else "true"))

    env = environment.copy()
    for key in ("GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_COMMON_DIR", "GIT_WORK_TREE"):
        env.pop(key, None)
    env.update(
        GIT_CONFIG_COUNT=str(len(transport)),
        GIT_CONFIG_GLOBAL=os.devnull,
        GIT_CONFIG_NOSYSTEM="1",
    )
    for index, (key, value) in enumerate(transport):
        env[f"GIT_CONFIG_KEY_{index}"] = key
        env[f"GIT_CONFIG_VALUE_{index}"] = value
    return env


def supports_config_hooks(version):
    fields = version.split()
    if len(fields) < 3:
        return False
    major, _, tail = fields[2].partition(".")
    minor = tail.partition(".")[0]
    return major.isdigit() and minor.isdigit() and (int(major), int(minor)) >= (2, 54)
