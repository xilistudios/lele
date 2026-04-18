package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/i18n"
)

func cronCmd() {
	if len(os.Args) < 3 {
		cronHelp()
		return
	}

	subcommand := os.Args[2]

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("%s\n", i18n.TPrintf("cli.common.errorLoadingConfig", err))
		return
	}

	cronStorePath := filepath.Join(cfg.WorkspacePath(), "cron", "jobs.json")

	switch subcommand {
	case "list":
		cronListCmd(cronStorePath)
	case "add":
		cronAddCmd(cronStorePath)
	case "remove":
		if len(os.Args) < 4 {
			fmt.Println(i18n.T("cli.cron.usageRemove"))
			return
		}
		cronRemoveCmd(cronStorePath, os.Args[3])
	case "enable":
		cronEnableCmd(cronStorePath, false)
	case "disable":
		cronEnableCmd(cronStorePath, true)
	default:
		fmt.Printf("%s\n", i18n.TPrintf("cli.common.unknownSubcommand", "cron", subcommand))
		cronHelp()
	}
}

func cronHelp() {
	fmt.Println(i18n.T("cli.cron.help.title"))
	fmt.Println(i18n.T("cli.cron.help.list"))
	fmt.Println(i18n.T("cli.cron.help.add"))
	fmt.Println(i18n.T("cli.cron.help.remove"))
	fmt.Println(i18n.T("cli.cron.help.enable"))
	fmt.Println(i18n.T("cli.cron.help.disable"))
	fmt.Println(i18n.T("cli.cron.help.addOptions"))
	fmt.Println(i18n.T("cli.cron.help.name"))
	fmt.Println(i18n.T("cli.cron.help.message"))
	fmt.Println(i18n.T("cli.cron.help.every"))
	fmt.Println(i18n.T("cli.cron.help.cron"))
	fmt.Println(i18n.T("cli.cron.help.deliver"))
	fmt.Println(i18n.T("cli.cron.help.to"))
	fmt.Println(i18n.T("cli.cron.help.channel"))
}

func cronListCmd(storePath string) {
	cs := cron.NewCronService(storePath, nil)
	jobs := cs.ListJobs(true)

	if len(jobs) == 0 {
		fmt.Println(i18n.T("cli.cron.noScheduledJobs"))
		return
	}

	fmt.Println(i18n.T("cli.cron.scheduledJobs"))
	fmt.Println(i18n.T("cli.cron.scheduledJobsSeparator"))
	for _, job := range jobs {
		var schedule string
		if job.Schedule.Kind == "every" && job.Schedule.EveryMS != nil {
			schedule = i18n.TPrintf("cli.cron.jobScheduleEvery", *job.Schedule.EveryMS/1000)
		} else if job.Schedule.Kind == "cron" {
			schedule = job.Schedule.Expr
		} else {
			schedule = i18n.T("cli.cron.jobScheduleOneTime")
		}

		nextRun := "scheduled"
		if job.State.NextRunAtMS != nil {
			nextTime := time.UnixMilli(*job.State.NextRunAtMS)
			nextRun = nextTime.Format("2006-01-02 15:04")
		}

		status := i18n.T("cli.cron.jobStatusEnabled")
		if !job.Enabled {
			status = i18n.T("cli.cron.jobStatusDisabled")
		}

		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobName", job.Name, job.ID))
		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobSchedule", schedule))
		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobStatus", status))
		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobNextRun", nextRun))
	}
}

func cronAddCmd(storePath string) {
	name := ""
	message := ""
	var everySec *int64
	cronExpr := ""
	deliver := false
	channel := ""
	to := ""

	args := os.Args[3:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n", "--name":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case "-m", "--message":
			if i+1 < len(args) {
				message = args[i+1]
				i++
			}
		case "-e", "--every":
			if i+1 < len(args) {
				var sec int64
				fmt.Sscanf(args[i+1], "%d", &sec)
				everySec = &sec
				i++
			}
		case "-c", "--cron":
			if i+1 < len(args) {
				cronExpr = args[i+1]
				i++
			}
		case "-d", "--deliver":
			deliver = true
		case "--to":
			if i+1 < len(args) {
				to = args[i+1]
				i++
			}
		case "--channel":
			if i+1 < len(args) {
				channel = args[i+1]
				i++
			}
		}
	}

	if name == "" {
		fmt.Println(i18n.T("cli.cron.errorNameRequired"))
		return
	}

	if message == "" {
		fmt.Println(i18n.T("cli.cron.errorMessageRequired"))
		return
	}

	if everySec == nil && cronExpr == "" {
		fmt.Println(i18n.T("cli.cron.errorScheduleRequired"))
		return
	}

	var schedule cron.CronSchedule
	if everySec != nil {
		everyMS := *everySec * 1000
		schedule = cron.CronSchedule{
			Kind:    "every",
			EveryMS: &everyMS,
		}
	} else {
		schedule = cron.CronSchedule{
			Kind: "cron",
			Expr: cronExpr,
		}
	}

	cs := cron.NewCronService(storePath, nil)
	job, err := cs.AddJob(name, schedule, message, deliver, channel, to)
	if err != nil {
		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.errorAddingJob", err))
		return
	}

	fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobAdded", job.Name, job.ID))
}

func cronRemoveCmd(storePath, jobID string) {
	cs := cron.NewCronService(storePath, nil)
	if cs.RemoveJob(jobID) {
		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobRemoved", jobID))
	} else {
		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobNotFound", jobID))
	}
}

func cronEnableCmd(storePath string, disable bool) {
	if len(os.Args) < 4 {
		fmt.Println(i18n.T("cli.cron.usageEnableDisable"))
		return
	}

	jobID := os.Args[3]
	cs := cron.NewCronService(storePath, nil)
	enabled := !disable

	job := cs.EnableJob(jobID, enabled)
	if job != nil {
		status := i18n.T("cli.cron.jobStatusEnabled")
		if disable {
			status = i18n.T("cli.cron.jobStatusDisabled")
		}
		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobEnabled", job.Name, status))
	} else {
		fmt.Printf("%s\n", i18n.TPrintf("cli.cron.jobNotFound", jobID))
	}
}
