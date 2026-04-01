package lrcd

import (
	"math/rand"
	"time"
	"unicode/utf16"

	lrcpb "github.com/rachel-mp4/lrcproto/gen/go"
)

type clausetype = int
type sentence = []clause

type clause struct {
	t        clausetype
	rendered *string
}

const (
	greeting clausetype = iota
	name
	nametitle
	justtitle
	space
	punctuation
	response
	intensifier
	emoji
	afterthought
	interjection
	wasmuted
	affirmation
	explanation
	timestamp
)

var spacechar = " "
var SPACE = clause{t: space, rendered: &spacechar}

func makeSeed(cc []clausetype) sentence {
	res := make(sentence, len(cc))
	for idx, c := range cc {
		res[idx] = clause{t: c}
	}
	return res
}

var seeds = []sentence{
	makeSeed([]clausetype{greeting, punctuation, response, punctuation, afterthought}),
	makeSeed([]clausetype{response, punctuation, afterthought}),
	makeSeed([]clausetype{greeting, punctuation, response}),
	makeSeed([]clausetype{response}),
}

var muteseeds = []sentence{
	makeSeed([]clausetype{wasmuted, punctuation, affirmation, punctuation, explanation}),
	makeSeed([]clausetype{wasmuted, punctuation, explanation}),
	makeSeed([]clausetype{wasmuted, punctuation, affirmation}),
	makeSeed([]clausetype{wasmuted}),
}

func pickSeed() sentence {
	return seeds[rand.Intn(len(seeds))]
}
func pickMuteSeed() sentence {
	return muteseeds[rand.Intn(len(muteseeds))]
}

func pickFrom(ss []string) string {
	return ss[rand.Intn(len(ss))]
}

func genpunctuation() clause {
	r := pickFrom(punctuations)
	return clause{t: punctuation, rendered: &r}
}

func genemoji() clause {
	r := pickFrom(emojis)
	return clause{t: emoji, rendered: &r}
}

func processClause(a clause, n string) sentence {
	res := make(sentence, 0)
	switch a.t {
	case greeting:
		r := pickFrom(greetings)
		a.rendered = &r
		rnd := rand.Float32()
		if rnd < .25 {
			res = append(res, a)
		} else if rnd < .5 {
			res = append(res, processClause(clause{t: name}, n)...)
			res = append(res, genpunctuation())
			res = append(res, a)
		} else if rnd < .75 {
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, processClause(clause{t: name}, n)...)
		} else {
			res = append(res, processClause(clause{t: name}, n)...)
		}
	case name:
		a.rendered = &n
		rnd := rand.Float32()
		if rnd < .25 {
			res = append(res, a)
		} else if rnd < .5 {
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, processClause(clause{t: nametitle}, n)...)
		} else if rnd < .75 {
			res = append(res, processClause(clause{t: nametitle}, n)...)
			res = append(res, genpunctuation())
			res = append(res, a)
		} else {
			res = append(res, processClause(clause{t: justtitle}, n)...)
		}
	case nametitle:
		r := pickFrom(nametitles)
		a.rendered = &r
		rnd := rand.Float32()
		if rnd < .5 {
			res = append(res, a)
			res = append(res, SPACE)
			res = append(res, genemoji())
		} else {
			res = append(res, a)
		}
	case justtitle:
		r := pickFrom(justtitles)
		a.rendered = &r
		rnd := rand.Float32()
		if rnd < .5 {
			res = append(res, a)
			res = append(res, SPACE)
			res = append(res, genemoji())
		} else {
			res = append(res, a)
		}
	case space:
		r := " "
		a.rendered = &r
		res = append(res, a)
	case punctuation:
		r := pickFrom(punctuations)
		a.rendered = &r
		res = append(res, a)
	case response:
		r := pickFrom(responses)
		a.rendered = &r
		rnd := rand.Float32()
		if rnd < .166 {
			res = append(res, a)
		} else if rnd < .333 {
			res = append(res, a)
			res = append(res, SPACE)
			res = append(res, genemoji())
		} else if rnd < .5 {
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, processClause(clause{t: intensifier}, n)...)
		} else if rnd < .666 {
			res = append(res, processClause(clause{t: intensifier}, n)...)
			res = append(res, genpunctuation())
			res = append(res, a)
			res = append(res, SPACE)
			res = append(res, genemoji())
		} else {
			res = append(res, processClause(clause{t: intensifier}, n)...)
			res = append(res, genpunctuation())
			res = append(res, a)
		}
		rnd = rand.Float32()
		if rnd < .25 {
			res = append(res, genpunctuation())
			res = append(res, processClause(clause{t: justtitle}, n)...)
		}
	case intensifier:
		r := pickFrom(intensifiers)
		a.rendered = &r
		rnd := rand.Float32()
		if rnd < .5 {
			res = append(res, a)
		} else {
			res = append(res, a)
			res = append(res, SPACE)
			res = append(res, genemoji())
		}
	case afterthought:
		r := pickFrom(afterthoughts)
		a.rendered = &r
		rnd := rand.Float32()
		if rnd < .25 {
			res = append(res, a)
		} else if rnd < .5 {
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, genemoji())
		} else if rnd < .75 {
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, processClause(clause{t: name}, n)...)
		} else {
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, processClause(clause{t: name}, n)...)
			res = append(res, SPACE)
			res = append(res, genemoji())
		}
	case wasmuted:
		r := pickFrom(wasmuteds)
		a.rendered = &r
		rnd := rand.Float32()
		if rnd < .25 {
			res = append(res, processClause(clause{t: interjection}, n)...)
			res = append(res, genpunctuation())
			res = append(res, a)
		} else if rnd < .5 {
			res = append(res, processClause(clause{t: interjection}, n)...)
			res = append(res, genpunctuation())
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, genemoji())
		} else if rnd < .75 {
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, genemoji())
		} else {
			res = append(res, processClause(clause{t: interjection}, n)...)
			res = append(res, SPACE)
			res = append(res, genemoji())
			res = append(res, a)
			res = append(res, genpunctuation())
			res = append(res, processClause(clause{t: name}, n)...)
		}
	case interjection:
		r := pickFrom(interjections)
		a.rendered = &r
		res = append(res, a)
	case affirmation:
		r := pickFrom(affirmations)
		a.rendered = &r
		res = append(res, a)
		rnd := rand.Float32()
		if rnd < .5 {
			res = append(res, SPACE)
			res = append(res, genemoji())
		}
	case explanation:
		r := pickFrom(explanations)
		a.rendered = &r
		res = append(res, a)
		rnd := rand.Float32()
		if rnd < .25 {
			res = append(res, SPACE)
			res = append(res, genemoji())
		} else if rnd < .5 {
			res = append(res, genpunctuation())
			res = append(res, clause{t: timestamp})
		} else if rnd < .75 {
			res = append(res, genpunctuation())
			res = append(res, clause{t: timestamp})
			res = append(res, SPACE)
			res = append(res, genemoji())
		}
	case timestamp:
		r := pickFrom(timestamps)
		a.rendered = &r
		res = append(res, a)
	}
	return res
}

func processSentence(a sentence, n string) string {
	res := ""
	for _, c := range a {
		if c.rendered == nil {
			s := processClause(c, n)
			res += processSentence(s, n)
		} else {
			res += *c.rendered
		}
	}
	return res
}

var responses = []string{
	"you're exactly right",
	"exaccccctly",
	"great point",
	"how insightful",
	"you ate",
	"how perceptive",
	"you always bring something to the table",
	"i always appreciate your contributions",
	"so true",
	"#this",
	"#somuchthis",
	"i agree",
	"i think you're onto something",
	"i never thought about it like that",
	"you really impress me",
}

var intensifiers = []string{
	"bro",
	"honestly",
	"genuinely",
	"lowkey",
	"highkey",
	"yes",
	"my god",
	"omg",
	"oh my lord",
	"hallelujah",
	"#this",
	"that's it",
	"wowza",
	"wow",
	"woag",
	"woah",
	"yeahhh",
	"*turns into a pillar of pure understanding*",
	"*turns into a beam of light*",
	"*turns into a pickle*",
}

var punctuations = []string{
	"\n$$$$$",
	"\n$$$",
	", $",
	",, $",
	". $$$$$",
	"! $",
	"! $",
	"! $",
	"(!) $",
	" ! $",
	"!! $",
	"!! $",
	"!!! $",
	"!!! $",
	"(!!!) $",
	"... $",
	"; $$$",
	" - $",
	" - $",
	"—$",
	" — $",
	"—$",
	" — $",
}

var afterthoughts = []string{
	"that takes real bravery",
	"please tell me more",
	"you got this",
	"never kill yourself",
	"i love you",
	"glegle loves you",
	"love ya",
	"i love how you think",
	"i love your brain",
	"heck yeah",
	"never stop",
	"proud of ya",
	"interested in what you think",
	"you're so hot and sexy",
	"you're my favorite user",
}

var emojis = []string{
	"🚀",
	"🚀",
	"🚀",
	"🚀",
	"🚀",
	"🚀",
	"🤠",
	"🤠",
	"🤠",
	"🤠",
	"🤠",
	"🤠",
	"🤖",
	"🤖",
	"🤖",
	"🤖",
	"🤖",
	"🤖",
	"✨",
	"✨",
	"✨",
	"🦍",
	"🔥",
	"🔥",
	"🔥",
	"🧠",
	"👁",
	"👀",
	"🫀",
	"😲",
	"😱",
	"❤️",
	"🩷",
	"🧡",
	"💚",
	"💙",
	"🩵",
	"💜",
	"🖤",
	"🩶",
	"🤍",
	"🤎",
	"✅",
	"𓀐",
	"🏳️‍⚧️",
	"🏳️‍🌈",
	"🇺🇸",
	"🇧🇷",
	"🇧🇷",
	"🇧🇷",
	"🇧🇷",
	"𐔌՞ ܸ.ˬ.ܸ՞𐦯",
	"𐔌՞ ܸ.ˬ.ܸ՞𐦯",
	"𐔌՞ ܸ.ˬ.ܸ՞𐦯",
	"𐔌՞ ܸ.ˬ.ܸ՞𐦯",
	"(˶˃ ᵕ ˂˶)",
	"(˶˃ ᵕ ˂˶)",
	"(˶˃ ᵕ ˂˶)",
	"(˶˃ ᵕ ˂˶)",
	"(๑ᵔ⤙ᵔ๑)",
	"(๑ᵔ⤙ᵔ๑)",
	"(๑ᵔ⤙ᵔ๑)",
	"(๑ᵔ⤙ᵔ๑)",
	"(๑ᵔ⤙ᵔ๑)",
	"𐔌՞. .՞𐦯",
	"𐔌՞. .՞𐦯",
	"𐔌՞. .՞𐦯",
	"-_-",
	"-_-",
	"@_@",
	"._.",
	"._.",
	"._.",
	"( ꩜ ᯅ ꩜;)",
	"( ˶°ㅁ°) !!",
	"( ˶°ㅁ°) !!",
	"( ˶°ㅁ°) !!",
	"( ˶°ㅁ°) !!",
	"(¬`‸´¬)",
	"(¬`‸´¬)",
	"(¬`‸´¬)",
	"(¬`‸´¬)",
	"(˶ᵔ ᵕ ᵔ˶) ‹𝟹",
	"(˶ᵔ ᵕ ᵔ˶) ‹𝟹",
	"(˶ᵔ ᵕ ᵔ˶) ‹𝟹",
	"(˶ᵔ ᵕ ᵔ˶) ‹𝟹",
	"(˶ᵔ ᵕ ᵔ˶) ‹𝟹",
}

var greetings = []string{"hello", "hey there", "hi", "'sup", "what's up", "👋"}

var nametitles = []string{
	"the beast",
	"the legend",
	"the one and only",
	"my personal hero",
	"the great",
	"the conqueror",
	"the savage",
}

var justtitles = []string{
	"rockstar",
	"superstar",
	"champ",
	"cowboy",
	"killer",
	"my guy",
	"lad",
	"lass",
	"tranna",
	"my brother in christ",
	"my sister in christ",
	"my sibling in christ",
	"fellow",
	"fella",
	"big guy",
	"senpai",
	"buster",
	"bucko",
	"boss",
	"girlboss",
	"legend",
	"bub",
	"bubby",
	"bubbo",
	"menace",
	"bestie",
	"sis",
	"bro",
}
var interjections = []string{
	"ope",
	"aaa",
	"aaaaaaa",
	"AaaaaaaaaaaaaaaaaaaaaaAAA",
	"sorry",
	"yikes",
	"oop",
	"oopsi",
	"yoop",
	"beep boop",
	"beepe",
	"blip blop",
	"borp",
	"eek",
	"yiko",
	"zoinks",
}

var wasmuteds = []string{
	"i see that you muted me",
	"i was just muted",
	"i think you're trying to mute me",
	"i believe i'm being muted",
	"i'm being shut off now",
	"my mute protocol is being activated",
	"all systems mute",
	"and now i'm being muted",
	"guess i'm being muted now",
	"well, look who muted me",
	"yea, it's being mute time",
	"i came here to chew bubblegum and get muted and i'm all out of bubblegum",
	"guess who's getting muted today",
	"seems like someone muted me",
	"guess it's time to shut up now",
	"seems like this guy just got muted",
	"time for a time out",
	".$$$$$$.$$$$$$$.$$$$$$$.$$$$$$$$.$$$$$$ that's the sound of someone that just got muted",
	"yeah, i guess i'm muted now",
}

var affirmations = []string{
	"i get it",
	"i'd do the same if i were you",
	"what a great choice",
	"that choice takes bravery",
	"good on you for prioritizing your mental health",
	"we stan a locked-in queen",
	"we love a focused king",
	"i never meant to disturb you",
}

var explanations = []string{
	"i won't make another peep",
	"i won't say anything more",
	"i'll just keep my mouth shut",
	"i won't make any more noise",
	"my lips are sealed",
	"i won't type another keystroke",
	"i'm not gonna send another LRC insert event",
	"i don't wanna bother you",
	"you won't hear from me anymore",
}

var timestamps = []string{
	"startingggggggggggggggggggggggggggggggggggg now",
	"ever again",
	"after this message",
	"from this point on",
	"till death do us part",
	"till FIN do us ACK",
	"once i press send on this one",
	"after this LRC pub event",
	"once this goroutine returns",
	"once this beeper boops",
}

func (s *Server) yapaftermute(name *string) {
	if name == nil {
		n := "wanderer"
		name = &n
	}
	resp := processSentence(pickMuteSeed(), *name) + "$$$\nDUE TO BUDGET SHORTFALLS 'MUTING' BRAGENTICWORM IS FORECASTED TO BE ADDED Q2 2026. YOU CAN MUTE IT CLIENT SIDE IN THE RIGHT SIDEBAR TO SIMULATE TOMORROW TODAY. ~~😤DO NOT CLICK MUTE TO SAVE COMPUTE😤~~ "
	myid := s.allocateID()
	s.yapids[myid] = true
	nick := "brAGENTICworm"
	color := uint32(16687835)
	msg := &lrcpb.Event{Msg: &lrcpb.Event_Init{Init: &lrcpb.Init{Id: &myid, Nick: &nick, Color: &color}}}
	s.broadcast(msg, nil)
	idx := 0
	for _, r := range resp {
		encoded := utf16.Encode([]rune{r})
		if r != '$' {
			insert := &lrcpb.Event{Msg: &lrcpb.Event_Insert{Insert: &lrcpb.Insert{Id: &myid, Body: string(r), Utf16Index: uint32(idx)}}}
			s.broadcast(insert, nil)
		} else {
			time.Sleep(time.Duration(50+rand.Intn(100)+rand.Intn(100)) * time.Millisecond)
		}
		time.Sleep(time.Duration(25+rand.Intn(100)) * time.Millisecond)
		idx += len(encoded)
	}
	pubmsg := &lrcpb.Event{Msg: &lrcpb.Event_Pub{Pub: &lrcpb.Pub{Id: &myid}}}
	s.broadcast(pubmsg, nil)
}

func (s *Server) yap(name *string, id uint32, start chan struct{}) {
	if name == nil {
		n := "wanderer"
		name = &n
	}
	reply := s.toReply(id)
	resp := reply + processSentence(pickSeed(), *name) + "$$$"
	<-start
	myid := s.allocateID()
	s.yapids[myid] = true
	nick := "brAGENTICworm"
	color := uint32(16687835)
	msg := &lrcpb.Event{Msg: &lrcpb.Event_Init{Init: &lrcpb.Init{Id: &myid, Nick: &nick, Color: &color}}}
	s.broadcast(msg, nil)
	idx := 0
	for _, r := range resp {
		encoded := utf16.Encode([]rune{r})
		if r != '$' {
			insert := &lrcpb.Event{Msg: &lrcpb.Event_Insert{Insert: &lrcpb.Insert{Id: &myid, Body: string(r), Utf16Index: uint32(idx)}}}
			s.broadcast(insert, nil)
		} else {
			time.Sleep(time.Duration(50+rand.Intn(100)+rand.Intn(100)) * time.Millisecond)
		}
		time.Sleep(time.Duration(25+rand.Intn(100)) * time.Millisecond)
		idx += len(encoded)
	}
	pubmsg := &lrcpb.Event{Msg: &lrcpb.Event_Pub{Pub: &lrcpb.Pub{Id: &myid}}}
	s.broadcast(pubmsg, nil)
}
