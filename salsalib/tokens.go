package salsalib

import (
	// "log"
	
	"salsacore"
)

func GetTokens(fid int) []salsacore.Token {	
	tokens := make([]salsacore.Token, 0, 100)
	/*
	db := GetDB()
	err := db.Select(&tokens, `SELECT fid, type, text, line, line_column, token_index FROM token 
								WHERE fid = ?`, fid)
	if err != nil {
		log.Printf("Error in GetTokens(%d): %v", fid, err)		
	}
	*/
	return tokens
}
