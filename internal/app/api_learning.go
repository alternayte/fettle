package app

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// SignalView is one steering signal.
type SignalView struct {
	ID      string    `json:"id"`
	Type    string    `json:"type"`
	RunID   string    `json:"run_id,omitempty"`
	BotID   string    `json:"bot_id,omitempty"`
	BotName string    `json:"bot_name,omitempty"`
	Step    string    `json:"step"`
	Skills  []string  `json:"skills"`
	Detail  string    `json:"detail"`
	At      time.Time `json:"at"`
}

// ScoreAverage is the average score of the runs that share one key.
type ScoreAverage struct {
	Key      string  `json:"key"`
	Runs     int     `json:"runs"`
	AvgScore float64 `json:"avg_score"`
}

// ScoreReport is the scores of a period with their averages per bot, skill, step and
// model (§14.2).
type ScoreReport struct {
	Days    int            `json:"days"`
	Runs    int            `json:"runs"`
	Average float64        `json:"average"`
	ByBot   []ScoreAverage `json:"by_bot"`
	BySkill []ScoreAverage `json:"by_skill"`
	ByStep  []ScoreAverage `json:"by_step"`
	ByModel []ScoreAverage `json:"by_model"`
}

func averages(groups map[string][]int) []ScoreAverage {
	out := []ScoreAverage{}
	for k, vs := range groups {
		sum := 0
		for _, v := range vs {
			sum += v
		}
		out = append(out, ScoreAverage{Key: k, Runs: len(vs), AvgScore: float64(sum) / float64(len(vs))})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runs != out[j].Runs {
			return out[i].Runs > out[j].Runs
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func (a *App) botNames(ctx context.Context, spaceID uuid.UUID) map[string]string {
	names := map[string]string{}
	if list, err := a.Bots.List(ctx, spaceID, true); err == nil {
		for _, b := range list {
			names[b.ID().String()] = b.Name
		}
	}
	return names
}

func (a *App) scoreReport(ctx context.Context, spaceID uuid.UUID, days int) (ScoreReport, error) {
	rep := ScoreReport{Days: days, ByBot: []ScoreAverage{}, BySkill: []ScoreAverage{}, ByStep: []ScoreAverage{}, ByModel: []ScoreAverage{}}
	list, err := a.Learning.Scores(ctx, spaceID, time.Now().AddDate(0, 0, -days))
	if err != nil {
		return rep, err
	}
	names := a.botNames(ctx, spaceID)
	bot, skill, step, model := map[string][]int{}, map[string][]int{}, map[string][]int{}, map[string][]int{}
	sum := 0
	for _, s := range list {
		sum += s.Value
		n := names[s.BotID.String()]
		if n == "" {
			n = s.BotID.String()
		}
		bot[n] = append(bot[n], s.Value)
		step[s.Step] = append(step[s.Step], s.Value)
		if s.ModelName != "" {
			model[s.ModelName] = append(model[s.ModelName], s.Value)
		}
		for _, k := range s.Skills {
			skill[k] = append(skill[k], s.Value)
		}
	}
	rep.Runs = len(list)
	if len(list) > 0 {
		rep.Average = float64(sum) / float64(len(list))
	}
	rep.ByBot, rep.BySkill, rep.ByStep, rep.ByModel = averages(bot), averages(skill), averages(step), averages(model)
	return rep, nil
}

func (a *App) registerLearning(api huma.API) {
	huma.Register(api, op("list-signals", http.MethodGet, "/spaces/{space}/learning/signals", "Steering signals of a space, newest first", "learning"), func(ctx context.Context, in *struct {
		SpacePath
		Days int `query:"days" minimum:"1" maximum:"365" default:"14"`
	}) (*struct {
		Body struct {
			Signals []SignalView `json:"signals"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		list, err := a.Learning.Signals(ctx, in.Space, time.Now().AddDate(0, 0, -in.Days), 500)
		if err != nil {
			return nil, err
		}
		names := a.botNames(ctx, in.Space)
		out := &struct {
			Body struct {
				Signals []SignalView `json:"signals"`
			}
		}{}
		out.Body.Signals = []SignalView{}
		for _, s := range list {
			v := SignalView{ID: s.ID().String(), Type: s.Type, Step: s.Step, Skills: s.Skills, Detail: s.Detail, At: s.CreatedAt()}
			if s.RunID != nil {
				v.RunID = s.RunID.String()
			}
			if s.BotID != nil {
				v.BotID, v.BotName = s.BotID.String(), names[s.BotID.String()]
			}
			out.Body.Signals = append(out.Body.Signals, v)
		}
		return out, nil
	})
	huma.Register(api, op("score-report", http.MethodGet, "/spaces/{space}/learning/scores", "Run scores of a period with averages per bot, skill, step and model", "learning"), func(ctx context.Context, in *struct {
		SpacePath
		Days int `query:"days" minimum:"1" maximum:"365" default:"30"`
	}) (*struct{ Body ScoreReport }, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		rep, err := a.scoreReport(ctx, in.Space, in.Days)
		if err != nil {
			return nil, err
		}
		return &struct{ Body ScoreReport }{Body: rep}, nil
	})
}
