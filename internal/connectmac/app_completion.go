package connectmac

import "fmt"

func (a App) runCompletion(configPath string, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "usage: cm completion <zsh|bash|fish|commands|profiles|apple-emails|aws-commands|mcp-commands|profile-commands>")
		return 2
	}
	switch args[0] {
	case "zsh":
		fmt.Fprint(a.Out, zshCompletionScript())
		return 0
	case "bash":
		fmt.Fprint(a.Out, bashCompletionScript())
		return 0
	case "fish":
		fmt.Fprint(a.Out, fishCompletionScript())
		return 0
	case "commands":
		printLines(a.Out, completionCommands())
		return 0
	case "aws-commands":
		printLines(a.Out, completionAWSCommands())
		return 0
	case "mcp-commands":
		printLines(a.Out, completionMCPCommands())
		return 0
	case "profile-commands":
		printLines(a.Out, completionProfileCommands())
		return 0
	case "profiles":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		printLines(a.Out, sortedProfileNames(cfg))
		return 0
	case "apple-emails":
		cfg, code := a.loadConfig(configPath)
		if code != 0 {
			return code
		}
		printLines(a.Out, sortedAppleEmails(cfg))
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown completion target %q\n", args[0])
		return 2
	}
}
func completionCommands() []string {
	return []string{
		"init",
		"init-rules",
		"guide",
		"version",
		"completion",
		"list",
		"profile",
		"next",
		"check",
		"connect",
		"open",
		"close",
		"ssh",
		"exec",
		"start",
		"pull",
		"push",
		"forget-host",
		"open-vnc",
		"setup-vnc",
		"stop",
		"status",
		"doctor",
		"dashboard",
		"job",
		"web",
		"mcp",
		"aws",
	}
}
func completionJobCommands() []string {
	return []string{"list", "status", "log", "wait"}
}
func completionProfileCommands() []string {
	return []string{"accounts", "find", "show", "add", "wizard", "remove", "rename", "edit", "export", "import", "import-dir"}
}
func completionAWSCommands() []string {
	return []string{
		"plan",
		"capacity",
		"open",
		"create",
		"status",
		"wait-ready",
		"adopt",
		"adopt-host",
		"launch-on-host",
		"destroy",
		"destroy-many",
		"destroy-all",
		"running",
	}
}
func completionMCPCommands() []string {
	return []string{"tools"}
}
func zshCompletionScript() string {
	return `#compdef cm

_cm_config_args() {
  local -a args
  args=()
  local i
  for (( i = 1; i < ${#words}; i++ )); do
    if [[ "${words[$i]}" == "--config" && -n "${words[$((i + 1))]}" ]]; then
      args=(--config "${words[$((i + 1))]}")
      break
    fi
  done
  echo "${args[@]}"
}

_cm_profiles() {
  local -a config_args values
  config_args=(${(z)$(_cm_config_args)})
  values=("${(@f)$(command cm completion profiles "${config_args[@]}" 2>/dev/null)}")
  compadd -- "${values[@]}"
}

_cm_profile_or_apple() {
  local -a config_args values
  config_args=(${(z)$(_cm_config_args)})
  values=("${(@f)$(command cm completion profiles "${config_args[@]}" 2>/dev/null)}")
  values+=("${(@f)$(command cm completion apple-emails "${config_args[@]}" 2>/dev/null)}")
  compadd -- "${values[@]}"
}

_cm() {
  local -a commands aws_commands mcp_commands job_commands profile_commands
  commands=("${(@f)$(command cm completion commands 2>/dev/null)}")
  aws_commands=("${(@f)$(command cm completion aws-commands 2>/dev/null)}")
  mcp_commands=("${(@f)$(command cm completion mcp-commands 2>/dev/null)}")
  job_commands=(list status log wait)
  profile_commands=("${(@f)$(command cm completion profile-commands 2>/dev/null)}")

  if [[ "${words[$((CURRENT - 1))]}" == "--config" ]]; then
    _files
    return
  fi

  if (( CURRENT == 2 )); then
    compadd -- "${commands[@]}"
    return
  fi

  case "${words[2]}" in
    check|connect|ssh|start|forget-host|open-vnc|setup-vnc|stop)
      (( CURRENT == 3 )) && _cm_profiles
      ;;
    open|close|next)
      (( CURRENT == 3 )) && _cm_profile_or_apple
      ;;
    guide)
      (( CURRENT == 3 )) && _values 'guide topic' first-use profile open close sync vnc mcp
      ;;
    pull)
      if (( CURRENT == 3 )); then
        _cm_profile_or_apple
      elif [[ "${words[$((CURRENT - 1))]}" == "--include" || "${words[$((CURRENT - 1))]}" == "--exclude" ]]; then
        _files
      else
        _values 'pull option' --include --exclude --config
      fi
      ;;
    push)
      if (( CURRENT == 3 )); then
        _cm_profile_or_apple
      elif (( CURRENT == 4 )); then
        _files
      elif [[ "${words[$((CURRENT - 1))]}" == "--include" || "${words[$((CURRENT - 1))]}" == "--exclude" ]]; then
        _files
      else
        _values 'push option' --include --exclude --config
      fi
      ;;
    exec)
      (( CURRENT == 3 )) && _cm_profiles
      ;;
    profile)
      if (( CURRENT == 3 )); then
        compadd -- "${profile_commands[@]}"
      elif [[ "${words[3]}" == "find" || "${words[3]}" == "show" || "${words[3]}" == "export" ]]; then
        local -a config_args apple_emails
        config_args=(${(z)$(_cm_config_args)})
        apple_emails=("${(@f)$(command cm completion apple-emails "${config_args[@]}" 2>/dev/null)}")
        values=("${(@f)$(command cm completion profiles "${config_args[@]}" 2>/dev/null)}")
        values+=("${apple_emails[@]}")
        compadd -- "${values[@]}"
      elif [[ "${words[3]}" == "remove" || "${words[3]}" == "rename" || "${words[3]}" == "edit" ]]; then
        _cm_profiles
      elif [[ "${words[3]}" == "import" || "${words[3]}" == "import-dir" ]]; then
        _files
      fi
      ;;
    aws)
      if (( CURRENT == 3 )); then
        compadd -- "${aws_commands[@]}"
      elif (( CURRENT == 4 )); then
        _cm_profile_or_apple
      else
        case "${words[$((CURRENT - 1))]}" in
          --host-id) ;;
          --except) _cm_profile_or_apple ;;
          *) _values 'aws option' --confirm --background --notify --all --host-id --except --config ;;
        esac
      fi
      ;;
    job)
      if (( CURRENT == 3 )); then
        compadd -- "${job_commands[@]}"
      fi
      ;;
    web)
      _values 'web option' --host --port --open --web-dir --config
      ;;
    mcp)
      if (( CURRENT == 3 )); then
        compadd -- "${mcp_commands[@]}"
      elif [[ "${words[3]}" == "tools" ]]; then
        _values 'mcp tools option' --json --config
      fi
      ;;
    completion)
      _values 'shell' zsh bash fish
      ;;
  esac
}

_cm "$@"
`
}
func bashCompletionScript() string {
	return `_cm_completion()
{
  local cur prev cmd sub config_args
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  cmd="${COMP_WORDS[1]}"
  config_args=()
  local i
  for (( i=1; i<COMP_CWORD; i++ )); do
    if [[ "${COMP_WORDS[i]}" == "--config" && -n "${COMP_WORDS[i+1]}" ]]; then
      config_args=(--config "${COMP_WORDS[i+1]}")
      break
    fi
  done

  if [[ "$prev" == "--config" ]]; then
    COMPREPLY=( $(compgen -f -- "$cur") )
    return 0
  fi

  if [[ $COMP_CWORD -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "$(cm completion commands 2>/dev/null)" -- "$cur") )
    return 0
  fi

  case "$cmd" in
    check|connect|ssh|start|forget-host|open-vnc|setup-vnc|stop|exec)
      [[ $COMP_CWORD -eq 2 ]] && COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      ;;
    open|close|next)
      [[ $COMP_CWORD -eq 2 ]] && COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      ;;
    guide)
      [[ $COMP_CWORD -eq 2 ]] && COMPREPLY=( $(compgen -W "first-use profile open close sync vnc mcp" -- "$cur") )
      ;;
    pull)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      elif [[ "$prev" == "--include" || "$prev" == "--exclude" ]]; then
        COMPREPLY=( $(compgen -f -- "$cur") )
      else
        COMPREPLY=( $(compgen -W "--include --exclude --config" -- "$cur") )
      fi
      ;;
    push)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      elif [[ "$prev" == "--include" || "$prev" == "--exclude" ]]; then
        COMPREPLY=( $(compgen -f -- "$cur") )
      else
        COMPREPLY=( $(compgen -W "--include --exclude --config" -- "$cur") )
      fi
      ;;
    profile)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profile-commands 2>/dev/null)" -- "$cur") )
      elif [[ "${COMP_WORDS[2]}" == "find" || "${COMP_WORDS[2]}" == "show" || "${COMP_WORDS[2]}" == "export" ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      elif [[ "${COMP_WORDS[2]}" == "remove" || "${COMP_WORDS[2]}" == "rename" || "${COMP_WORDS[2]}" == "edit" ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      elif [[ "${COMP_WORDS[2]}" == "import" || "${COMP_WORDS[2]}" == "import-dir" ]]; then
        COMPREPLY=( $(compgen -f -- "$cur") )
      fi
      ;;
    aws)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion aws-commands 2>/dev/null)" -- "$cur") )
      elif [[ $COMP_CWORD -eq 3 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
      else
        if [[ "$prev" == "--except" ]]; then
          COMPREPLY=( $(compgen -W "$(cm completion profiles "${config_args[@]}" 2>/dev/null; cm completion apple-emails "${config_args[@]}" 2>/dev/null)" -- "$cur") )
        else
          COMPREPLY=( $(compgen -W "--confirm --background --notify --all --host-id --except --config" -- "$cur") )
        fi
      fi
      ;;
    job)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "list status log wait" -- "$cur") )
      fi
      ;;
    web)
      COMPREPLY=( $(compgen -W "--host --port --open --web-dir --config" -- "$cur") )
      ;;
    mcp)
      if [[ $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "$(cm completion mcp-commands 2>/dev/null)" -- "$cur") )
      elif [[ "${COMP_WORDS[2]}" == "tools" ]]; then
        COMPREPLY=( $(compgen -W "--json --config" -- "$cur") )
      fi
      ;;
    completion)
      COMPREPLY=( $(compgen -W "zsh bash fish" -- "$cur") )
      ;;
  esac
  return 0
}
complete -F _cm_completion cm
`
}
func fishCompletionScript() string {
	return `complete -c cm -f
complete -c cm -n "not __fish_seen_subcommand_from (cm completion commands)" -a "(cm completion commands)"
complete -c cm -n "__fish_seen_subcommand_from check connect ssh start forget-host open-vnc setup-vnc stop exec" -a "(cm completion profiles)"
complete -c cm -n "__fish_seen_subcommand_from open close next" -a "(cm completion profiles)"
complete -c cm -n "__fish_seen_subcommand_from open close next" -a "(cm completion apple-emails)"
complete -c cm -n "__fish_seen_subcommand_from guide" -a "first-use profile open close sync vnc mcp"
complete -c cm -n "__fish_seen_subcommand_from pull push" -a "(cm completion profiles)"
complete -c cm -n "__fish_seen_subcommand_from pull push" -a "(cm completion apple-emails)"
complete -c cm -n "__fish_seen_subcommand_from pull push" -a "--include --exclude"
complete -c cm -n "__fish_seen_subcommand_from profile; and not __fish_seen_subcommand_from (cm completion profile-commands)" -a "(cm completion profile-commands)"
complete -c cm -n "__fish_seen_subcommand_from profile; and __fish_seen_subcommand_from find" -a "(cm completion apple-emails)"
complete -c cm -n "__fish_seen_subcommand_from aws; and not __fish_seen_subcommand_from (cm completion aws-commands)" -a "(cm completion aws-commands)"
complete -c cm -n "__fish_seen_subcommand_from aws; and __fish_seen_subcommand_from (cm completion aws-commands)" -a "(cm completion profiles)"
complete -c cm -n "__fish_seen_subcommand_from aws; and __fish_seen_subcommand_from (cm completion aws-commands)" -a "(cm completion apple-emails)"
complete -c cm -n "__fish_seen_subcommand_from aws; and __fish_seen_subcommand_from destroy" -a "--background --notify"
complete -c cm -n "__fish_seen_subcommand_from job; and not __fish_seen_subcommand_from list status log wait" -a "list status log wait"
complete -c cm -n "__fish_seen_subcommand_from web" -a "--host --port --open --web-dir --config"
complete -c cm -n "__fish_seen_subcommand_from mcp; and not __fish_seen_subcommand_from (cm completion mcp-commands)" -a "(cm completion mcp-commands)"
complete -c cm -n "__fish_seen_subcommand_from mcp; and __fish_seen_subcommand_from tools" -a "--json"
complete -c cm -n "__fish_seen_subcommand_from completion" -a "zsh bash fish"
`
}
