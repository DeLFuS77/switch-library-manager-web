package web

import "testing"

// Names of demos the titles database does not flag, and names that only look alike.
func TestDemoNames(t *testing.T) {
	demos := []string{
		"1993 Shenandoah Demo", "49 Keys Demo", "Abyss Memory Fallen Angel and the Path of Magic-DEMO",
		"Asterix & Obelix XXL3: The Crystal Menhir - Demo", "The Legend of Zelda™: Breath of the Wild Demo for KIOSK",
		"【DEMO】DORAEMON　NOBITA NO BOKUJOMONOGATARI", "Pixel Puzzle Makeout League (Demo!)", "Vitamin Connection DEMO!",
		"The Last Friend: DEMO EDITION", "Splatoon™ 2 Special Demo 2020", "Splatoon 2: Demo especial 2020",
		"Game (Demo)", "Game [Trial Version]", "Game demo version", "疯狂兔子：奇遇派对 - 试玩版", "Jeu Démo",
	}
	for _, name := range demos {
		if !demoName.MatchString(name) {
			t.Errorf("%q is a demo", name)
		}
	}
	games := []string{
		"100 Demon Fantasia", "Amnesia: Memories", "Angelo and Deemon: One Hell of a Quest", "Demolition Racer",
		"Bayonetta Origins: Cereza and the Lost Demon", "Democracy 4", "Beyond Memories - Darkness of the Soul",
	}
	for _, name := range games {
		if demoName.MatchString(name) {
			t.Errorf("%q is not a demo", name)
		}
	}
}
